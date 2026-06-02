# Phase 1 Spec: Transfers

**Phase:** 1 - Monolith  
**Priority:** P3  
**Status:** Draft  
**Last updated:** 2026-06-02

## Purpose

Implement safe wallet-to-wallet transfers inside the Phase 1 monolith. Transfers own balance mutation orchestration: debit the source wallet, credit the destination wallet, create a transfer record, and create matching ledger entries in one database transaction.

## Scope

Transfers include:

- `POST /v1/transfers`.
- Positive integer-cent transfer amounts.
- Source wallet ownership validation.
- Atomic debit and credit in one Postgres transaction.
- Row-level locking to prevent race conditions.
- Deadlock avoidance through deterministic lock ordering.
- Insufficient funds handling with unchanged balances.
- Creation of corresponding debit and credit ledger entries.

Transfers exclude external payments, async processing, Kafka events, saga orchestration, fraud scoring, scheduled transfers, and cross-currency transfers.

## API

### `POST /v1/transfers`

Request body:

```json
{
  "from_wallet_id": "77787174-e221-49de-bf16-5834e0d250a1",
  "to_wallet_id": "a572d276-3292-4c0e-b4f8-e5256d2d814c",
  "amount": 1250
}
```

Success response: `201 Created`

```json
{
  "id": "68e09292-b720-4cfd-a99d-c9f265dcb59b",
  "from_wallet_id": "77787174-e221-49de-bf16-5834e0d250a1",
  "to_wallet_id": "a572d276-3292-4c0e-b4f8-e5256d2d814c",
  "amount": 1250,
  "status": "completed",
  "created_at": "2026-06-02T00:00:00Z",
  "balances": {
    "from": 3750,
    "to": 6250
  }
}
```

## Domain Rules

- `amount` must be greater than `0`.
- `from_wallet_id` and `to_wallet_id` must be valid UUIDs.
- Source and destination wallets must be different.
- The authenticated user must own the source wallet.
- Destination wallet must exist.
- Source and destination wallets must use the same currency.
- Source balance must be greater than or equal to `amount`.
- Balances must remain unchanged if any validation or database write fails.
- A completed transfer must have exactly two ledger entries: one debit and one credit.

Phase 1 allows transfers to wallets owned by other users. It only requires ownership of the source wallet.

## Transaction Flow

```text
POST /v1/transfers
  -> handler parses and validates request shape
  -> service loads authenticated principal
  -> service begins transaction
  -> service locks both wallet rows in deterministic UUID order
  -> service verifies source ownership, destination existence, same currency, and sufficient funds
  -> repo updates source balance
  -> repo updates destination balance
  -> repo inserts transfer row
  -> repo inserts debit ledger entry with source balance_after
  -> repo inserts credit ledger entry with destination balance_after
  -> service commits transaction
  -> handler returns transfer response
```

If any step fails after `BEGIN`, rollback the transaction.

## Locking Strategy

Use `SELECT ... FOR UPDATE` on both wallet rows before reading balances for mutation. Always lock rows in deterministic order by UUID string or database ordering to avoid deadlocks when two concurrent transfers touch the same pair of wallets in opposite directions.

The service must map lock/read results back to source and destination roles after locking.

## Database

Transfer schema:

```sql
CREATE TABLE transfers (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  from_wallet_id UUID NOT NULL REFERENCES wallets(id),
  to_wallet_id UUID NOT NULL REFERENCES wallets(id),
  amount BIGINT NOT NULL CHECK (amount > 0),
  status TEXT NOT NULL DEFAULT 'completed',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT transfers_distinct_wallets CHECK (from_wallet_id <> to_wallet_id)
);
```

Indexes:

```sql
CREATE INDEX transfers_from_wallet_id_idx ON transfers(from_wallet_id);
CREATE INDEX transfers_to_wallet_id_idx ON transfers(to_wallet_id);
CREATE INDEX transfers_created_at_idx ON transfers(created_at DESC);
```

## Error Responses

| Condition | Status | Code |
|---|---:|---|
| Invalid UUID | `400` | `invalid_request` |
| Amount <= 0 | `422` | `invalid_amount` |
| Same source and destination wallet | `422` | `invalid_transfer` |
| Source wallet missing or not owned | `404` | `wallet_not_found` |
| Destination wallet missing | `404` | `wallet_not_found` |
| Currency mismatch | `422` | `currency_mismatch` |
| Insufficient funds | `422` | `insufficient_funds` |

## Testing Requirements

- Handler tests cover request validation and service error mapping.
- Service unit tests cover happy path, insufficient funds, same-wallet transfer, source ownership mismatch, missing destination, and currency mismatch.
- Integration tests against Postgres cover successful transfer and insufficient funds rollback.
- Concurrency integration test should run competing transfers against the same source wallet and verify the final balance never goes negative.

## Acceptance Criteria

- Successful transfer updates both balances in one transaction.
- Successful transfer returns transfer ID, amount, timestamp, and resulting balances.
- Successful transfer creates one debit ledger entry and one credit ledger entry.
- Insufficient funds returns `422` and leaves both balances unchanged.
- Concurrent transfers cannot produce negative balances.
- Source wallet ownership is enforced.
