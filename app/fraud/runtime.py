import asyncio
import inspect
from collections.abc import Awaitable, Callable


class WorkerHealth:
    def __init__(
        self,
        *,
        kafka_check: Callable[[], Awaitable[bool]],
        database_check: Callable[[], Awaitable[bool]],
        schema_check: Callable[[], Awaitable[bool]],
        provider_check: Callable[[], bool],
        live_check: Callable[[], bool] | None = None,
    ) -> None:
        self._kafka_check = kafka_check
        self._database_check = database_check
        self._schema_check = schema_check
        self._provider_check = provider_check
        self._live_check = live_check or (lambda: True)

    def liveness(self) -> dict[str, bool]:
        return {"live": self._live_check()}

    async def readiness(self) -> dict[str, bool]:
        kafka, database, schema = await asyncio.gather(
            self._kafka_check(), self._database_check(), self._schema_check()
        )
        try:
            provider = self._provider_check()
        except Exception:
            provider = False
        return {
            "ready": kafka and database and schema and provider,
            "kafka": kafka,
            "database": database,
            "schema": schema,
            "provider": provider,
        }


class WorkerRuntime:
    def __init__(
        self, *, runner, database, grpc_clients=(), producer=None, health=None
    ) -> None:
        self._runner = runner
        self._database = database
        self._grpc_clients = tuple(grpc_clients)
        self._producer = producer
        self.health = health
        self._run_task: asyncio.Task | None = None
        self._live = False

    async def run(self) -> None:
        self._run_task = asyncio.current_task()
        self._live = True
        try:
            await self._runner.run()
        except asyncio.CancelledError:
            pass
        finally:
            self._live = False

    async def shutdown(self, *, timeout_seconds: float = 30.0) -> None:
        await self._runner.shutdown()
        task = self._run_task
        if task is not None and task is not asyncio.current_task():
            try:
                await asyncio.wait_for(asyncio.shield(task), timeout=timeout_seconds)
            except TimeoutError:
                task.cancel()
                await task
        for client in self._grpc_clients:
            await _close(client)
        if self._producer is not None:
            await _close(self._producer, method="stop")
        await _close(self._database)

    def is_live(self) -> bool:
        return self._live


async def _close(value, *, method: str = "close") -> None:
    result = getattr(value, method)()
    if inspect.isawaitable(result):
        await result


def provider_configuration_ready(environ=None) -> bool:
    from app.llm.config import load_provider_registry_config
    from app.llm.registry import DriverRegistry

    try:
        DriverRegistry.from_config(load_provider_registry_config(environ))
        return True
    except Exception:
        return False
