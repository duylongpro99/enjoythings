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
| Hand-issued certificates with no rotation | `internal/mtls` reloads the leaf and CA before every handshake; `mtls.certManager.*` in the Helm chart issues and renews a `Certificate` per service; see `docs/design-notes/phase5-cert-rotation.md` |
| No automated checks | `.github/workflows/ci.yml` runs the Go, Python, and web suites |
| Python had no linter or type checker | `ruff` and `mypy` configured in `pyproject.toml`, both clean |
| `web/` had no tests and floating dependencies | Vitest suite for the chat route, versions pinned to the lockfile |
| Operator review UI | `ListFraudReviews` / `GetFraudReview` on the orchestrator, `GET /v1/fraud-reviews` and `GET /v1/fraud-reviews/{payment_id}` on the gateway, `/admin/fraud-reviews` in `web/`; see `docs/design-notes/phase5-operator-review-ui.md` |
| Reviews with no deadline | `ExpireFraudReviews` on the orchestrator, swept by `RunReviewReaper` when `SAGA_FRAUD_REVIEW_TTL` is set; see `docs/design-notes/phase5-review-reaper-and-redrive.md` |
| Dead letters nobody consumed | `cmd/dlq-redrive` with `list`, `redrive`, and `discard` over `internal/deadletter.Decode`/`Replay` |
| Fraud audit rows and dead-letter records kept forever | `SAGA_FRAUD_AUDIT_RETENTION` sweeper on the orchestrator, `FRAUD_AUDIT_RETENTION_DAYS` sweeper in the fraud worker, `retention.ms` on every `*.dlq` topic; all keep-forever by default; see `docs/design-notes/phase5-retention.md` |

## Still open

### SPIFFE or mesh-issued workload identity

Certificates now rotate without a restart and, in Kubernetes, are issued and
renewed by cert-manager from a per-cluster CA. What remains is identity beyond a
DNS-name leaf from one CA: no `spiffe://` URI SANs, no trust-domain federation
across clusters, no mesh enforcing per-identity policy, and no support for an
issuer that does not write `ca.crt` into the Secret. The Python fraud worker
still reads its certificate once and needs a rollout restart within the
`renewBefore` window, because grpc-python has no client-side reload hook.

### Reviewer assignment and fraud-worker enrichment

The review page shows the verdict the saga holds and its audit trail, but not
the enrichment behind the verdict — velocity metrics, sanitized facts, and the
raw model response stay in the fraud worker's `fraud_sessions` table, keyed by
the `fraud_session_id` the page displays. Reading them would mean the
orchestrator opening a second database or the gateway calling a new Python
endpoint. There is also no reviewer assignment: nothing on the saga row models
an assignee, and adding one is a write path with its own audit semantics.

