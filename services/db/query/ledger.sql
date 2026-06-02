-- name: CreateLedgerEntry :one
INSERT INTO ledger_entries (wallet_id, transfer_id, direction, amount, balance_after)
VALUES ($1, $2, $3, $4, $5)
RETURNING id, wallet_id, transfer_id, direction, amount, balance_after, created_at;

-- name: ListLedgerEntries :many
SELECT id, wallet_id, transfer_id, direction, amount, balance_after, created_at
FROM ledger_entries
WHERE wallet_id = $1
  AND (
    sqlc.narg('cursor_created_at')::timestamptz IS NULL
    OR (created_at, id) < (sqlc.narg('cursor_created_at')::timestamptz, sqlc.narg('cursor_id')::uuid)
  )
ORDER BY created_at DESC, id DESC
LIMIT $2;
