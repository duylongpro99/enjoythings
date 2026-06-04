CREATE TABLE IF NOT EXISTS sagas (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  payment_id TEXT NOT NULL UNIQUE,
  idempotency_key TEXT NOT NULL,
  command_type TEXT NOT NULL DEFAULT 'StartPaymentSaga',
  user_id TEXT NOT NULL,
  from_wallet_id TEXT NOT NULL,
  to_wallet_id TEXT NOT NULL,
  amount_cents BIGINT NOT NULL CHECK (amount_cents > 0),
  currency TEXT NOT NULL,
  state TEXT NOT NULL,
  last_error TEXT NOT NULL DEFAULT '',
  trace_id TEXT NOT NULL DEFAULT '',
  wallet_debit_id TEXT NOT NULL DEFAULT '',
  ledger_reservation_id TEXT NOT NULL DEFAULT '',
  transfer_id TEXT NOT NULL DEFAULT '',
  failure_code TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (user_id, command_type, idempotency_key)
);

CREATE INDEX IF NOT EXISTS sagas_non_terminal_updated_idx
  ON sagas (updated_at, id)
  WHERE state NOT IN ('COMPLETED', 'FAILED');
