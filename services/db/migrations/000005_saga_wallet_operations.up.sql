CREATE TABLE IF NOT EXISTS saga_wallet_operations (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  payment_id UUID NOT NULL,
  operation TEXT NOT NULL CHECK (operation IN ('debit', 'compensation')),
  idempotency_key TEXT NOT NULL,
  wallet_id UUID NOT NULL REFERENCES wallets(id),
  amount_cents BIGINT NOT NULL CHECK (amount_cents > 0),
  currency CHAR(3) NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('completed')),
  balance_after_cents BIGINT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT saga_wallet_operations_payment_operation_unique UNIQUE (payment_id, operation),
  CONSTRAINT saga_wallet_operations_idempotency_operation_unique UNIQUE (idempotency_key, operation)
);

CREATE INDEX IF NOT EXISTS saga_wallet_operations_wallet_created_idx
  ON saga_wallet_operations(wallet_id, created_at DESC);
