CREATE INDEX IF NOT EXISTS fraud_sessions_completed_at_idx
  ON fraud_sessions (completed_at)
  WHERE completed_at IS NOT NULL;
