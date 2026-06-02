-- name: CreateWallet :one
INSERT INTO wallets (user_id, currency)
VALUES ($1, $2)
RETURNING id, user_id, balance, currency, created_at, updated_at;

-- name: GetWallet :one
SELECT id, user_id, balance, currency, created_at, updated_at
FROM wallets
WHERE id = $1;

-- name: LockWalletsForTransfer :many
SELECT id, user_id, balance, currency, created_at, updated_at
FROM wallets
WHERE id = ANY($1::uuid[])
ORDER BY id
FOR UPDATE;

-- name: UpdateWalletBalance :one
UPDATE wallets
SET balance = $2, updated_at = now()
WHERE id = $1
RETURNING id, user_id, balance, currency, created_at, updated_at;
