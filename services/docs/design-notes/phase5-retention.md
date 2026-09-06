# Compliance Retention

Problem: Three stores grew without bound. The saga orchestrator writes a
`saga_fraud_audit_records` row for every fraud event it sees — orphans,
duplicates, transitions, deferred rail results, operator decisions — and never
deletes one. The Python fraud worker keeps every `fraud_sessions` row in its
TimescaleDB, raw model response included. And poison Kafka records parked on
`<topic>.dlq` inherited the broker's default `retention.ms`, which is a
transport setting nobody chose for evidence. None of this had a policy an
operator could point to, and the safe answer to "how long do we keep this" was
"until the disk fills".

Structure: Each store gets one knob and one mechanism, and every knob defaults to
the behaviour that existed before — keep forever — so turning retention on is
an explicit decision recorded in configuration, never a side effect of an
upgrade.

The orchestrator reads `SAGA_FRAUD_AUDIT_RETENTION`, a Go duration; zero or
unset leaves the sweeper off. When set, `saga.FraudAuditSweeper` runs beside the
outbox publisher in `cmd/saga-orchestrator`: once an hour it calls
`DeleteFraudAuditBefore(cutoff, 1000)` until a batch comes back short, so a long
backlog drains in bounded deletes rather than one statement holding locks
against live audit writes. The delete joins `sagas` and skips any row whose saga
is still non-terminal. That rule is deliberate: a `deferred_terminal` record is
the only copy of a rail result the saga has not applied yet — it is what the
operability note points at for manual re-drive when a resume fails midway — and
a saga can sit in `FRAUD_REVIEW` for longer than any retention window because
automatic rejection is still out of scope. Rows for `COMPLETED` and `FAILED`
sagas, and orphan rows whose payment never had a saga, age out normally.
Migration `000012` adds the `created_at` index the cutoff scan needs. Deleted
rows are counted in `saga_fraud_audit_rows_deleted_total`.

The fraud worker reads `FRAUD_AUDIT_RETENTION_DAYS`; zero keeps everything.
`fraud_sessions` is a plain table, not a hypertable, and cannot become one
without cost: Timescale requires the partitioning column in every unique
constraint, and `source_event_id UNIQUE` is exactly what makes a redelivered
request find its existing session instead of scoring twice. So instead of
`add_retention_policy`, `app/fraud/retention.py` runs the same shape of loop as
the Go sweeper inside `WorkerRuntime`, deleting sessions whose `completed_at` is
past the cutoff in batches of 1000. Sessions that never completed are kept: an
unfinished row is a lease another worker may still take over, and its
`source_event_id` is the dedup key for the retry. Migration `000002` adds a
partial index on `completed_at`.

Dead-letter topics get an explicit `retention.ms` at creation — `--config` on
`kafka-topics.sh --create` — followed by `kafka-configs.sh --alter` so a
changed value reaches topics that already exist. Compose reads
`KAFKA_DLQ_RETENTION_MS`; the Helm chart reads `kafka.dlqRetentionMs` and
applies it to every topic in `kafka.topics` that ends in `.dlq`. The default is
30 days, long enough to notice a poisoned producer and redrive its records, and
short enough that a forgotten DLQ does not become an archive. Source topics are
untouched and keep the broker default.

Tradeoffs: The keep-forever default means retention only exists where an
operator turned it on — the same posture as mTLS and RS256, and for the same
reason: deleting evidence must be a choice, not a surprise. The cost is that a
fresh deployment still grows unbounded until someone sets the variables, which
the README now says plainly.

Keeping audit rows for non-terminal sagas makes the sweeper's promise "rows
older than N for sagas that are finished", not "no row older than N". A saga
stuck in review for a year keeps its year-old audit trail. That is the right
failure mode for an audit table, and the stuck saga itself is the problem the
review-deadline backlog item exists to solve.

Deleting an audit row also removes the `event_id` that `ON CONFLICT DO
NOTHING` used to swallow a redelivered event. A Kafka redelivery of an event
older than the retention window would therefore write a fresh row — for an
event the saga has long since reached a terminal state on, so it is recorded as
`ignored` or `duplicate` against that state. That is a correct record of what
happened, not a double-apply, and the window is far longer than any consumer
group's redelivery horizon.

Sweep interval and batch size are constants, not configuration. One env var per
store is the whole operator surface; if a deployment ever needs to tune the
sweep cadence, that is the moment to add a variable, with the evidence in hand.
Finally, `kafka-topics.sh --create --if-not-exists` alone would not change a
topic that already exists, which is why the `--alter` step follows it in both
Compose and the chart's post-install Job — the value in configuration is the
value on the broker after every `up` or `helm upgrade`, not only the first.
