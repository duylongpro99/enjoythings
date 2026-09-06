-- The operator review detail reads one payment's whole audit trail in order.
CREATE INDEX IF NOT EXISTS saga_fraud_audit_records_payment_idx
  ON saga_fraud_audit_records (payment_id, created_at);
