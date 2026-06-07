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

-- name: ListFraudTransactionHistory :many
SELECT le.direction, le.amount, w.currency, le.created_at
FROM ledger_entries le
JOIN wallets w ON w.id = le.wallet_id
WHERE le.wallet_id = $1
ORDER BY le.created_at DESC, le.id DESC
LIMIT $2;

-- name: GetFraudVelocityMetrics :one
WITH recent AS (
  SELECT amount
  FROM ledger_entries
  WHERE ledger_entries.wallet_id = sqlc.arg('fraud_wallet_id')
    AND ledger_entries.created_at >= sqlc.arg('as_of')::timestamptz - interval '1 hour'
    AND ledger_entries.created_at <= sqlc.arg('as_of')::timestamptz
),
thirty_day AS (
  SELECT le.amount,
    CASE
      WHEN le.wallet_id = t.from_wallet_id THEN t.to_wallet_id
      ELSE t.from_wallet_id
    END AS counterparty_wallet_id
  FROM ledger_entries le
  JOIN transfers t ON t.id = le.transfer_id
  WHERE le.wallet_id = sqlc.arg('fraud_wallet_id')
    AND le.created_at >= sqlc.arg('as_of')::timestamptz - interval '30 days'
    AND le.created_at <= sqlc.arg('as_of')::timestamptz
)
SELECT
  COALESCE((SELECT count(*) FROM recent), 0)::int AS transactions_last_hour,
  COALESCE((SELECT sum(amount) FROM recent), 0)::bigint AS amount_last_hour_cents,
  COALESCE((SELECT avg(amount)::bigint FROM thirty_day), 0)::bigint AS average_amount_30d_cents,
  COALESCE((SELECT count(DISTINCT counterparty_wallet_id) FROM thirty_day), 0)::int AS distinct_recipients_30d;
