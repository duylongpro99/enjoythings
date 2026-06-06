ALTER TABLE sagas
  DROP COLUMN IF EXISTS deferred_payment_json,
  DROP COLUMN IF EXISTS fraud_flagged_at,
  DROP COLUMN IF EXISTS fraud_reason,
  DROP COLUMN IF EXISTS fraud_risk_score,
  DROP COLUMN IF EXISTS fraud_action,
  DROP COLUMN IF EXISTS fraud_session_id;
