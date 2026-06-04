-- name: LockWalletForUpdate :one
SELECT id, user_id, balance, currency, created_at, updated_at
FROM wallets
WHERE id = $1
FOR UPDATE;

-- name: GetSagaWalletOperation :one
SELECT id, payment_id, operation, idempotency_key, wallet_id, amount_cents, currency, status, balance_after_cents, created_at, updated_at
FROM saga_wallet_operations
WHERE payment_id = $1 AND operation = $2;

-- name: GetSagaWalletOperationForUpdate :one
SELECT id, payment_id, operation, idempotency_key, wallet_id, amount_cents, currency, status, balance_after_cents, created_at, updated_at
FROM saga_wallet_operations
WHERE payment_id = $1 AND operation = $2
FOR UPDATE;

-- name: CreateSagaWalletOperation :one
INSERT INTO saga_wallet_operations (
  payment_id,
  operation,
  idempotency_key,
  wallet_id,
  amount_cents,
  currency,
  status,
  balance_after_cents
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING id, payment_id, operation, idempotency_key, wallet_id, amount_cents, currency, status, balance_after_cents, created_at, updated_at;
