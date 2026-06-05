CREATE TABLE IF NOT EXISTS verifications (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id TEXT NOT NULL UNIQUE,
  verification_id TEXT NOT NULL UNIQUE,
  idempotency_key TEXT NOT NULL,
  payment_id TEXT NOT NULL DEFAULT '',
  trace_id TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL,
  reason TEXT NOT NULL DEFAULT '',
  decided_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT verifications_status_check
    CHECK (status IN ('pending', 'verified', 'rejected'))
);

CREATE UNIQUE INDEX IF NOT EXISTS verifications_idempotency_key_idx
  ON verifications (idempotency_key);
