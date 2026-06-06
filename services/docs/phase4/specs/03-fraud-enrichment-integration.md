# Phase 4.3: Fraud Enrichment Integration

**Priority:** P3 - Go/Python integration boundary
**Session size:** One to two implementation sessions
**Depends on:** P1, P2 ports

## Goal

Provide fraud enrichment through narrow Go-owned RPCs and a thin Python client without leaking protobuf details into the graph.

## Scope

- Implement Ledger fraud transaction-history and velocity RPCs.
- Reuse Verification `GetStatus` for KYC status.
- Add `app/fraud/integrations/grpc_client.py`.
- Map protobuf responses into sanitized fraud DTOs.
- Add deadlines, error mapping, and unit/integration tests.

## Ledger Behavior

`GetFraudTransactionHistory`:

- Validates wallet ID and requires `limit` from `1` through `100`; the Python client sends `FRAUD_HISTORY_LIMIT`, default `20`.
- Returns newest-first summaries.
- Omits all raw identifiers and balance fields.

`GetFraudVelocityMetrics` evaluates all windows relative to one UTC `as_of` timestamp captured by the handler before repository calls:

- Returns counts and amount aggregates for the last hour and 30-day average data.
- Returns zero-valued metrics when no history exists.
- Performs aggregation inside Ledger because Ledger owns the transaction data.
- Repository methods receive the captured `as_of` value so one-hour and 30-day windows cannot drift during a request.

The RPC handlers add focused sqlc queries to the existing Ledger repository. They do not expose a generic query API or add raw SQL to the gRPC handler.

## Python gRPC Client

`GrpcFraudDataClient` is the only fraud module that imports generated protobuf modules.

- `get_transaction_history` calls Ledger and returns `TransactionHistoryEntry` DTOs.
- `get_velocity_metrics` calls Ledger and returns `VelocityMetrics`.
- `get_kyc_status` calls Verification and maps status to `KYCStatus`.
- Each RPC receives private raw identifiers from the worker request and returns sanitized DTOs.
- gRPC metadata carries W3C trace context after P7.
- Each call has a 2-second deadline.
- The client retries `UNAVAILABLE` once after a 100-millisecond delay. It does not retry invalid arguments, not-found Ledger wallets, or deadline-exceeded responses.

## Error Handling

- Invalid private identifiers become non-retryable enrichment errors.
- `NOT_FOUND` KYC maps to `unverified`, not an infrastructure failure.
- `UNAVAILABLE`, deadline exceeded, and unexpected server failures become retryable enrichment errors after the client policy is exhausted.
- A missing Ledger wallet is a non-retryable enrichment error because scoring facts cannot be established.
- The scoring workflow performs no hidden gRPC retries.

## Testing

- Go handler tests verify response sanitization and aggregate correctness.
- Python mapping tests use fake generated stubs.
- A boundary test fails if `app/fraud` modules outside `integrations/grpc_client.py` import generated protobuf packages.
- Integration tests verify Ledger and Verification calls without a real model.
- Repository integration tests verify one-hour and 30-day boundary calculations with an injected `as_of` timestamp.

## Acceptance Criteria

- The graph can use a fake `FraudDataPort`.
- Python never queries Go-owned databases.
- Enrichment responses contain no wallet, user, transfer, or ledger-entry IDs.
- RPC deadlines and failure classifications are explicit and tested.
