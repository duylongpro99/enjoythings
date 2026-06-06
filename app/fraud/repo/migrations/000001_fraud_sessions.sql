CREATE TABLE IF NOT EXISTS fraud_sessions (
  session_id TEXT PRIMARY KEY,
  source_event_id TEXT NOT NULL UNIQUE,
  payment_id TEXT NOT NULL,
  provider_id TEXT NOT NULL DEFAULT '',
  model_id TEXT NOT NULL DEFAULT '',
  sanitized_facts_json JSONB NOT NULL DEFAULT '{}'::jsonb,
  enrichment_json JSONB NOT NULL DEFAULT '{}'::jsonb,
  raw_llm_response TEXT NOT NULL DEFAULT '',
  parsed_verdict_json JSONB NOT NULL DEFAULT '{}'::jsonb,
  events_json JSONB NOT NULL DEFAULT '[]'::jsonb,
  final_outcome TEXT NOT NULL DEFAULT '',
  failure_reason TEXT NOT NULL DEFAULT '',
  output_event_type TEXT NOT NULL DEFAULT '',
  output_published BOOLEAN NOT NULL DEFAULT FALSE,
  lease_owner TEXT NOT NULL DEFAULT '',
  lease_expires_at TIMESTAMPTZ,
  started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  completed_at TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS fraud_sessions_payment_id_idx
  ON fraud_sessions (payment_id);

CREATE INDEX IF NOT EXISTS fraud_sessions_claimable_idx
  ON fraud_sessions (lease_expires_at)
  WHERE completed_at IS NULL;
