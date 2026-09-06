# Fraud Review Deadline and Dead-Letter Redrive

Problem: Phase 5 gave a fraud review an exit and gave poison Kafka records a
home, but both stopped one step short of unattended operation. A review that no
operator ever decided still held the payer's money indefinitely; the Phase 4
scope note had ruled a 24-hour automatic rejection out of scope, and the
`RejectFraudReview` decision was deliberately given an actor ID so a system
actor could one day take it. And every consumer parked records it could not
decode on `<topic>.dlq` with the raw bytes, coordinates, and decode error, yet
nothing read those topics, so a parked record was preserved evidence with no
path back into the system.

Structure: The deadline is `ExpireFraudReviews` on the orchestrator. It lists
the non-terminal sagas the store already exposes for boot-time resumption,
keeps those in `FRAUD_REVIEW` whose fraud flag timestamp is older than the TTL,
and rejects each through `RejectFraudReview` with the actor
`system:fraud-review-reaper` and a reason naming the TTL. Nothing about the
rejection is new: the wallet refund, ledger cancellation, `fraud_rejected`
failure code, `tx.failed` event, and `review_rejected` audit record are the ones
a manual rejection produces. Only the actor and a `fraud_review_expired` saga
event distinguish an expiry. `RunReviewReaper` sweeps on a ticker, and the saga
orchestrator starts it only when `SAGA_FRAUD_REVIEW_TTL` is positive, so the
default deployment behaves exactly as before.

Redrive is an operator command, `cmd/dlq-redrive`, over two functions added to
`internal/deadletter`: `Decode` reads a dead-letter payload and refuses any
schema version it does not know, and `Replay` builds the record to produce back
to the source topic, keeping the producer's key so partitioning is unchanged and
adding an `x-dead-letter-redrive` header with the dead-letter coordinates. The
command reads `<topic>.dlq` through a consumer group of its own, which is what
makes "pending" a defined notion: `list` shows pending records and commits
nothing, `redrive` produces the replay and then commits, `discard` commits
alone. A decision commits only after it has taken effect, so a failed replay is
seen again next run. Records are decided in order, and `--max 1 --value-file`
is how an operator replays one record with corrected bytes.

Tradeoffs: The reaper measures the deadline from the fraud flag timestamp, not
from when the saga entered review, because that is the timestamp the saga
carries. The two coincide for every saga flagged while in `PAYMENT_PROCESSING`,
which is the only transition into review. Rows written before the column
existed fall back to `updated_at`, which for a saga still in review is that same
transition.

The reaper lists every non-terminal saga rather than querying for overdue
reviews specifically. That keeps the `Store` interface unchanged and reuses a
query that already runs at every boot; a dedicated query is the obvious next
step if the non-terminal set ever grows large enough for a minute-by-minute
sweep to matter.

Redrive is ordered, not addressable. Choosing a record by offset while leaving
earlier ones pending is not expressible with a committed consumer offset, and
inventing a side table to make it expressible would be more machinery than the
problem deserves. An operator who wants to skip a record discards it, and the
dead-letter record itself is unchanged on the topic for as long as retention
keeps it. Replayed records reach idempotent consumers, and the redrive header
makes a replay visible to anyone reading the source topic.
