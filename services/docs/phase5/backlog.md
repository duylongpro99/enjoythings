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

## Still open

### Workload identity and certificate rotation

mTLS now authenticates services to each other, but the certificates are issued
by a local script and distributed by hand (a mounted Secret in Kubernetes, a
bind mount in Compose). There is no rotation, no short-lived credentials, and no
workload-identity provider — cert-manager, SPIFFE, or a mesh — issuing and
rotating them automatically. A leaf expires on its `CERT_DAYS` horizon and must
be reissued and the pods restarted.

### Operator review UI

Fraud review decisions are REST calls that require an `admin` role claim. There
is no queue view, no reviewer assignment, and no way to see the enrichment that
produced a verdict without querying the fraud audit database directly.

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
