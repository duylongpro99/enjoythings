CREATE TABLE IF NOT EXISTS saga_fraud_audit_records (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  event_id TEXT NOT NULL UNIQUE,
  payment_id TEXT NOT NULL,
  kind TEXT NOT NULL,
  saga_state TEXT NOT NULL DEFAULT '',
  details_json TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

