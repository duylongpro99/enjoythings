# Phase 1 Spec Priorities

This folder splits Phase 1 into focused, independently reviewable specs. Use the priorities below to decide implementation order and review focus.

## Priority Levels

| Priority | Meaning |
|---|---|
| `P0` | Required before feature work can be implemented safely. |
| `P1` | Required early because later specs depend on the contract or boundary. |
| `P2` | Core business capability with direct user-visible behavior. |
| `P3` | Core money movement capability that depends on wallets and auth. |
| `P4` | Audit/read capability that depends on transfer records and wallet ownership. |

## Spec Priority Table

| Order | Priority | Spec | Why |
|---:|---|---|---|
| 1 | `P0` | `phase1-foundation.md` | Creates the runnable Go service, config, Postgres, migrations, health checks, and local dev path. |
| 2 | `P0` | `phase1-testing.md` | Defines the verification gates and should shape implementation from the start, not after features are written. |
| 3 | `P1` | `phase1-api-contract.md` | Locks route shapes, status codes, and error envelopes before handlers are implemented. |
| 4 | `P1` | `phase1-auth.md` | Protects all `/v1/*` routes and provides user identity for ownership checks. |
| 5 | `P2` | `phase1-wallets.md` | Establishes wallet lifecycle, ownership, and balance reads. |
| 6 | `P3` | `phase1-transfers.md` | Implements transactional money movement and owns balance mutation orchestration. |
| 7 | `P4` | `phase1-ledger.md` | Adds immutable audit reads after transfers can produce ledger entries. |

## Recommended Implementation Sequence

1. Build the foundation and keep the service runnable.
2. Set up the test strategy and minimal test harness before business logic.
3. Lock the API contract for request, response, and error shapes.
4. Add authentication middleware and principal extraction.
5. Implement wallet creation and balance reads.
6. Implement transfers with row locking and transaction safety.
7. Implement ledger pagination and audit reads.
8. Run the full verification gate from `phase1-testing.md` before considering Phase 1 complete.

## Boundary Rule

`transfers` owns balance mutation orchestration, `ledger` owns immutable audit records, and `wallets` owns wallet lifecycle plus balance reads. Keep this boundary intact so the later microservice split remains clean.
