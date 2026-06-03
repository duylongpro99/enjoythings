CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE IF NOT EXISTS wallets (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID NOT NULL,
  balance BIGINT NOT NULL DEFAULT 0,
  currency CHAR(3) NOT NULL DEFAULT 'USD',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT wallets_non_negative_balance CHECK (balance >= 0)
);

CREATE INDEX IF NOT EXISTS wallets_user_id_idx ON wallets(user_id);

CREATE TABLE IF NOT EXISTS transfers (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  from_wallet_id UUID NOT NULL REFERENCES wallets(id),
  to_wallet_id UUID NOT NULL REFERENCES wallets(id),
  amount BIGINT NOT NULL CHECK (amount > 0),
  status TEXT NOT NULL DEFAULT 'completed',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT transfers_distinct_wallets CHECK (from_wallet_id <> to_wallet_id)
);

CREATE INDEX IF NOT EXISTS transfers_from_wallet_id_idx ON transfers(from_wallet_id);
CREATE INDEX IF NOT EXISTS transfers_to_wallet_id_idx ON transfers(to_wallet_id);
CREATE INDEX IF NOT EXISTS transfers_created_at_idx ON transfers(created_at DESC);

CREATE TABLE IF NOT EXISTS ledger_entries (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  wallet_id UUID NOT NULL REFERENCES wallets(id),
  transfer_id UUID NOT NULL REFERENCES transfers(id),
  direction TEXT NOT NULL CHECK (direction IN ('debit', 'credit')),
  amount BIGINT NOT NULL CHECK (amount > 0),
  balance_after BIGINT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS ledger_entries_wallet_created_idx
  ON ledger_entries(wallet_id, created_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS ledger_entries_transfer_id_idx
  ON ledger_entries(transfer_id);
