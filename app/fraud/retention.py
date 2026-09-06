import asyncio
import logging
from collections.abc import Callable
from datetime import UTC, datetime, timedelta
from typing import Protocol

DEFAULT_SWEEP_INTERVAL_SECONDS = 3600.0
DEFAULT_SWEEP_BATCH_SIZE = 1000


class RetentionStore(Protocol):
    async def delete_completed_before(self, cutoff: datetime, limit: int) -> int: ...


class AuditRetentionSweeper:
    """Deletes completed fraud sessions past the retention window.

    `fraud_sessions` is a plain table rather than a hypertable — its primary
    key and the `source_event_id` uniqueness that dedups redeliveries cannot
    include a time column — so retention is a bounded, periodic delete in the
    worker instead of Timescale's `add_retention_policy`. A retention of zero
    days keeps every row and turns the sweeper into a no-op.
    """

    def __init__(
        self,
        store: RetentionStore,
        *,
        retention_days: int,
        interval_seconds: float = DEFAULT_SWEEP_INTERVAL_SECONDS,
        batch_size: int = DEFAULT_SWEEP_BATCH_SIZE,
        now: Callable[[], datetime] | None = None,
    ) -> None:
        self._store = store
        self._retention = timedelta(days=retention_days)
        self._interval_seconds = interval_seconds
        self._batch_size = batch_size
        self._now = now or (lambda: datetime.now(UTC))
        self._logger = logging.getLogger("app.fraud.retention")

    @property
    def enabled(self) -> bool:
        return self._retention > timedelta(0)

    async def sweep(self) -> int:
        """Drain every expired session in bounded batches; return rows deleted."""
        if not self.enabled:
            return 0
        cutoff = self._now() - self._retention
        deleted = 0
        while True:
            count = await self._store.delete_completed_before(cutoff, self._batch_size)
            deleted += count
            if count < self._batch_size:
                break
        if deleted:
            self._logger.info(
                "fraud audit retention sweep deleted sessions",
                extra={"deleted": deleted, "cutoff": cutoff.isoformat()},
            )
        return deleted

    async def run(self) -> None:
        if not self.enabled:
            return
        while True:
            try:
                await self.sweep()
            except asyncio.CancelledError:
                raise
            except Exception:
                self._logger.exception("fraud audit retention sweep failed")
            await asyncio.sleep(self._interval_seconds)
