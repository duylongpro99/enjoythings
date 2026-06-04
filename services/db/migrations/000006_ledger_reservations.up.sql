CREATE TABLE IF NOT EXISTS ledger_transfer_reservations (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  payment_id UUID NOT NULL UNIQUE,
  idempotency_key TEXT NOT NULL,
  trace_id TEXT NOT NULL DEFAULT '',
  from_wallet_id UUID NOT NULL REFERENCES wallets(id),
  to_wallet_id UUID NOT NULL REFERENCES wallets(id),
  amount_cents BIGINT NOT NULL CHECK (amount_cents > 0),
  currency CHAR(3) NOT NULL,
  status TEXT NOT NULL CHECK (status IN ('RESERVED', 'CONFIRMED', 'CANCELED')),
  transfer_id UUID REFERENCES transfers(id),
  wallet_debit_id UUID,
  completed_at TIMESTAMPTZ,
  canceled_at TIMESTAMPTZ,
  cancel_reason TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT ledger_transfer_reservations_distinct_wallets CHECK (from_wallet_id <> to_wallet_id)
);

CREATE INDEX IF NOT EXISTS ledger_transfer_reservations_status_idx
  ON ledger_transfer_reservations(status);
