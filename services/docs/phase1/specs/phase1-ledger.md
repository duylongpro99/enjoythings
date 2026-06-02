# Phase 1 Spec: Ledger

**Phase:** 1 - Monolith  
**Priority:** P4  
**Status:** Draft  
**Last updated:** 2026-06-02

## Purpose

Provide an immutable audit trail for wallet balance changes. In Phase 1, the transfer service writes ledger entries synchronously in the same database transaction as balance updates.

## Scope

Ledger includes:

- `GET /v1/ledger/:wallet_id`.
- Debit and credit entries for completed transfers.
- Append-only storage.
- `balance_after` snapshots for point-in-time audit.
- Pagination by creation time and stable ID tie-breaker.
- Wallet ownership enforcement for reads.

Ledger excludes event sourcing infrastructure, Kafka consumers, Redis projections, manual ledger adjustments, reversals, and accounting reports.

## API

### `GET /v1/ledger/:wallet_id`

Query parameters:

| Parameter | Required | Default | Notes |
|---|---:|---|---|
| `limit` | No | `50` | Minimum `1`, maximum `100`. |
| `cursor` | No | none | Opaque cursor returned by previous page. |

Success response: `200 OK`

```json
{
  "wallet_id": "77787174-e221-49de-bf16-5834e0d250a1",
  "entries": [
    {
      "id": "bc1f6d24-ff58-4fc7-8493-c5d380035b79",
      "transfer_id": "68e09292-b720-4cfd-a99d-c9f265dcb59b",
      "direction": "debit",
      "amount": 1250,
      "balance_after": 3750,
      "created_at": "2026-06-02T00:00:00Z"
    }
  ],
  "next_cursor": null
}
```

Entries should be returned newest first by `created_at DESC, id DESC`.

## Domain Rules

- Every completed transfer creates exactly two ledger entries.
- Debit entry belongs to the source wallet.
- Credit entry belongs to the destination wallet.
- `amount` is always positive; `direction` carries debit/credit meaning.
- `balance_after` records the wallet balance immediately after the entry is applied.
- Ledger rows are append-only. Application code must not update or delete them.
- Only the wallet owner can read a wallet's ledger in Phase 1.

## Database

Ledger schema:

```sql
CREATE TABLE ledger_entries (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  wallet_id UUID NOT NULL REFERENCES wallets(id),
  transfer_id UUID NOT NULL REFERENCES transfers(id),
  direction TEXT NOT NULL CHECK (direction IN ('debit', 'credit')),
  amount BIGINT NOT NULL CHECK (amount > 0),
  balance_after BIGINT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
```

Indexes:

```sql
CREATE INDEX ledger_entries_wallet_created_idx
  ON ledger_entries(wallet_id, created_at DESC, id DESC);

CREATE INDEX ledger_entries_transfer_id_idx
  ON ledger_entries(transfer_id);
```

Append-only enforcement is primarily an application rule in Phase 1. Database permissions or triggers can be added later if needed, but they are not required for this phase.

## Pagination

Use cursor pagination instead of offset pagination so ledger reads remain stable as new entries are added.

Cursor contents:

```json
{
  "created_at": "2026-06-02T00:00:00Z",
  "id": "bc1f6d24-ff58-4fc7-8493-c5d380035b79"
}
```

Encode the cursor as URL-safe base64 JSON. Invalid cursors return `400 invalid_cursor`.

Pagination query logic:

```sql
WHERE wallet_id = @wallet_id
  AND (
    @cursor_created_at IS NULL
    OR (created_at, id) < (@cursor_created_at, @cursor_id)
  )
ORDER BY created_at DESC, id DESC
LIMIT @limit_plus_one;
```

Fetch `limit + 1` rows to determine whether `next_cursor` exists.

## Error Responses

| Condition | Status | Code |
|---|---:|---|
| Invalid wallet UUID | `400` | `invalid_request` |
| Invalid cursor | `400` | `invalid_cursor` |
| Invalid limit | `400` | `invalid_request` |
| Wallet not found or not owned | `404` | `wallet_not_found` |

## Testing Requirements

- Handler tests cover wallet ID parsing, limit validation, cursor validation, and response mapping.
- Service tests cover ownership enforcement and pagination boundaries.
- Repository integration tests cover ordering, cursor pagination, and filtering by wallet ID.
- Transfer integration tests verify ledger entries are created atomically with balance updates.

## Acceptance Criteria

- Ledger endpoint returns entries for the authenticated user's wallet.
- Ledger endpoint does not expose another user's entries.
- Transfer creates debit and credit entries with correct `balance_after` values.
- Ledger entries are returned newest first.
- Cursor pagination returns stable pages with a valid `next_cursor` when more rows exist.
- Application code never updates or deletes ledger entries.
