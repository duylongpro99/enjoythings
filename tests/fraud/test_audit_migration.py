from pathlib import Path


def test_fraud_audit_migration_uses_dedicated_schema_without_raw_user_or_wallet_ids() -> None:
    migration = Path("app/fraud/repo/migrations/000001_fraud_sessions.sql")
    sql = migration.read_text()

    assert "CREATE TABLE IF NOT EXISTS fraud_sessions" in sql
    assert "source_event_id TEXT NOT NULL UNIQUE" in sql
    assert "payment_id TEXT NOT NULL" in sql
    assert "user_id" not in sql
    assert "wallet_id" not in sql
    assert "raw_llm_response" in sql
    assert "lease_expires_at" in sql
    assert "output_published" in sql
