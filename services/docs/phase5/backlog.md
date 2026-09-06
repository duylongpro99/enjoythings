# Phase 5 Backlog

Deliberate deferrals that remain after the Phase 5 operability work. Each entry
names what exists today and what closing it would require, so the decision to
schedule it can be made from this file alone.

## Closed in Phase 5

| Item | Where it landed |
| --- | --- |
| Sagas stuck in `FRAUD_REVIEW` | `ResumeFraudReview` / `RejectFraudReview` on the orchestrator, `POST /v1/payments/{id}/fraud-review/{resume,reject}` on the gateway |
| Poison Kafka records committed and lost | `internal/deadletter` and per-topic `<topic>.dlq` topics, Go and Python consumers |
| HS256-only token verification | `JWT_ALG=RS256` with `JWT_PUBLIC_KEY_PEM` or `JWT_PUBLIC_KEY_FILE` |
| Internal gRPC on the trusted-network assumption | `GRPC_TLS_ENABLED` (`FRAUD_GRPC_TLS_ENABLED` for the worker) behind `internal/mtls`; certs from `make certs`; see `docs/design-notes/phase5-mtls.md` |
| No automated checks | `.github/workflows/ci.yml` runs the Go, Python, and web suites |
| Python had no linter or type checker | `ruff` and `mypy` configured in `pyproject.toml`, both clean |
| `web/` had no tests and floating dependencies | Vitest suite for the chat route, versions pinned to the lockfile |
| Operator review UI | `ListFraudReviews` / `GetFraudReview` on the orchestrator, `GET /v1/fraud-reviews` and `GET /v1/fraud-reviews/{payment_id}` on the gateway, `/admin/fraud-reviews` in `web/`; see `docs/design-notes/phase5-operator-review-ui.md` |

## Still open

### Workload identity and certificate rotation

mTLS now authenticates services to each other, but the certificates are issued
by a local script and distributed by hand (a mounted Secret in Kubernetes, a
bind mount in Compose). There is no rotation, no short-lived credentials, and no
workload-identity provider — cert-manager, SPIFFE, or a mesh — issuing and
rotating them automatically. A leaf expires on its `CERT_DAYS` horizon and must
be reissued and the pods restarted.

### Reviewer assignment and fraud-worker enrichment

The review page shows the verdict the saga holds and its audit trail, but not
the enrichment behind the verdict — velocity metrics, sanitized facts, and the
raw model response stay in the fraud worker's `fraud_sessions` table, keyed by
the `fraud_session_id` the page displays. Reading them would mean the
orchestrator opening a second database or the gateway calling a new Python
endpoint. There is also no reviewer assignment: nothing on the saga row models
an assignee, and adding one is a write path with its own audit semantics.

### Automatic rejection after a review deadline

`docs/phase4/specs/00-phase4-scope-and-decisions.md` lists 24-hour automatic
rejection as out of scope, and it still is. A reaper over sagas in
`FRAUD_REVIEW` past a configured TTL would reuse `RejectFraudReview` with a
system actor, which is why the decision takes an actor ID rather than assuming
a human.

### Dead-letter redrive

Poison records are parked on `<topic>.dlq` with everything needed to replay
them, but nothing consumes those topics. A redrive tool would read a dead-letter
record, let an operator correct or discard it, and produce it back to the source
topic.

### Compliance retention

Fraud audit rows and dead-letter records accumulate without a retention policy.
Real retention windows and deletion are still out of scope.
