import asyncio
from datetime import UTC, datetime, timedelta
from pathlib import Path

import pytest

from app.fraud.config import FraudConfig, FraudConfigError
from app.fraud.repo.postgres import PostgresFraudSessionStore
from app.fraud.retention import AuditRetentionSweeper
from app.fraud.runtime import WorkerRuntime

NOW = datetime(2026, 9, 1, 12, tzinfo=UTC)


def test_config_keeps_audit_forever_by_default_and_rejects_negative_days() -> None:
    base = {"FRAUD_DATABASE_URL": "postgres://fraud-db"}

    assert FraudConfig.from_env(base).audit_retention_days == 0
    assert FraudConfig.from_env({**base, "FRAUD_AUDIT_RETENTION_DAYS": "90"}).audit_retention_days == 90

    with pytest.raises(FraudConfigError, match="FRAUD_AUDIT_RETENTION_DAYS"):
        FraudConfig.from_env({**base, "FRAUD_AUDIT_RETENTION_DAYS": "-1"})
    with pytest.raises(FraudConfigError, match="numeric settings"):
        FraudConfig.from_env({**base, "FRAUD_AUDIT_RETENTION_DAYS": "ninety"})


def test_sweeper_drains_expired_sessions_in_bounded_batches() -> None:
    store = CountingStore(expired=25)
    sweeper = AuditRetentionSweeper(store, retention_days=30, batch_size=10, now=lambda: NOW)

    deleted = asyncio.run(sweeper.sweep())

    assert deleted == 25
    assert [limit for _, limit in store.calls] == [10, 10, 10]
    assert {cutoff for cutoff, _ in store.calls} == {NOW - timedelta(days=30)}


def test_sweeper_with_zero_retention_never_touches_the_store() -> None:
    store = CountingStore(expired=5)
    sweeper = AuditRetentionSweeper(store, retention_days=0, now=lambda: NOW)

    assert sweeper.enabled is False
    assert asyncio.run(sweeper.sweep()) == 0
    asyncio.run(sweeper.run())
    assert store.calls == []


def test_sweeper_survives_store_failures_and_keeps_running() -> None:
    async def scenario() -> int:
        store = FailingOnceStore()
        sweeper = AuditRetentionSweeper(
            store, retention_days=1, interval_seconds=0.001, now=lambda: NOW
        )
        task = asyncio.create_task(sweeper.run())
        while store.calls < 3:
            await asyncio.sleep(0.001)
        task.cancel()
        with pytest.raises(asyncio.CancelledError):
            await task
        return store.calls

    assert asyncio.run(scenario()) >= 3


def test_runtime_stops_the_sweeper_when_the_consumer_loop_ends() -> None:
    async def scenario() -> bool:
        store = CountingStore(expired=0)
        sweeper = AuditRetentionSweeper(store, retention_days=1, interval_seconds=60)
        runtime = WorkerRuntime(runner=FinishedRunner(), database=store, retention=sweeper)
        await runtime.run()
        await asyncio.sleep(0)
        return all(task.done() for task in asyncio.all_tasks() - {asyncio.current_task()})

    assert asyncio.run(scenario()) is True


def test_store_deletes_only_completed_sessions_before_cutoff_and_reports_count() -> None:
    pool = FakePool(execute_results=["DELETE 3"])
    store = PostgresFraudSessionStore(pool, lease_owner="worker-1")

    deleted = asyncio.run(store.delete_completed_before(NOW, 100))

    assert deleted == 3
    sql, args = pool.calls[0]
    assert "DELETE FROM fraud_sessions" in sql
    assert "completed_at IS NOT NULL AND completed_at < $1" in sql
    assert "LIMIT $2" in sql
    assert args == (NOW, 100)


def test_retention_migration_indexes_completed_sessions() -> None:
    sql = Path("app/fraud/repo/migrations/000002_fraud_sessions_retention.sql").read_text()

    assert "CREATE INDEX IF NOT EXISTS fraud_sessions_completed_at_idx" in sql
    assert "WHERE completed_at IS NOT NULL" in sql


class CountingStore:
    def __init__(self, *, expired: int) -> None:
        self.remaining = expired
        self.calls: list[tuple[datetime, int]] = []

    async def delete_completed_before(self, cutoff: datetime, limit: int) -> int:
        self.calls.append((cutoff, limit))
        deleted = min(limit, self.remaining)
        self.remaining -= deleted
        return deleted

    async def close(self) -> None:
        pass


class FailingOnceStore:
    def __init__(self) -> None:
        self.calls = 0

    async def delete_completed_before(self, cutoff: datetime, limit: int) -> int:
        self.calls += 1
        if self.calls == 1:
            raise RuntimeError("database unavailable")
        return 0


class FinishedRunner:
    async def run(self) -> None:
        return None

    async def shutdown(self) -> None:
        return None


class FakePool:
    def __init__(self, execute_results=None) -> None:
        self.execute_results = list(execute_results or [])
        self.calls: list[tuple[str, tuple[object, ...]]] = []

    async def execute(self, sql: str, *args):
        self.calls.append((sql, args))
        return self.execute_results.pop(0)
