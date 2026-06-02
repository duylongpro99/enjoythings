-- name: CreateTransfer :one
INSERT INTO transfers (from_wallet_id, to_wallet_id, amount)
VALUES ($1, $2, $3)
RETURNING id, from_wallet_id, to_wallet_id, amount, status, created_at;
