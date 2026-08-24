import asyncio
import os
import signal
from uuid import uuid4

import asyncpg
from aiokafka import AIOKafkaConsumer, AIOKafkaProducer
from prometheus_client import start_http_server

from app.fraud.completion import CompletionService
from app.fraud.config import FraudConfig
from app.fraud.integrations.grpc_client import build_grpc_fraud_data_client
from app.fraud.integrations.kafka import (
    AIOKafkaConsumerAdapter,
    KafkaDeadLetterPublisher,
    KafkaFraudPublisher,
    KafkaWorkerRunner,
)
from app.fraud.repo.postgres import PostgresFraudSessionStore
from app.fraud.runtime import WorkerHealth, WorkerRuntime, provider_configuration_ready
from app.fraud.service import FraudScoringService
from app.fraud.tracing import init_tracing
from app.fraud.worker import FraudWorker
from app.llm.config import load_provider_registry_config
from app.llm.registry import DriverRegistry


async def build_runtime(environ=None) -> WorkerRuntime:
    source = environ if environ is not None else os.environ
    config = FraudConfig.from_env(source)
    shutdown_tracing = init_tracing("fraud-worker", source)
    provider_config = load_provider_registry_config(source)
    registry = DriverRegistry.from_config(provider_config)
    provider = next(
        item for item in provider_config.providers if item.id == provider_config.default_provider_id
    )
    completion = CompletionService(
        registry.default_driver, provider_id=provider.id, model_id=provider.model
    )

    pool = await asyncpg.create_pool(config.database_url)
    store = PostgresFraudSessionStore(pool, lease_owner=str(uuid4()))
    data, grpc_channels = build_grpc_fraud_data_client(
        config.ledger_grpc_addr, config.verification_grpc_addr
    )
    producer = AIOKafkaProducer(bootstrap_servers=config.kafka_bootstrap_servers)
    consumer = AIOKafkaConsumer(
        config.request_topic,
        bootstrap_servers=config.kafka_bootstrap_servers,
        group_id=config.consumer_group,
        enable_auto_commit=False,
    )
    await producer.start()
    await consumer.start()
    publisher = KafkaFraudPublisher(
        producer, provider_id=provider.id, model_id=provider.model
    )
    service = FraudScoringService(
        data, completion, config, store=store, provider_id=provider.id
    )
    worker = FraudWorker(service, publisher, store)
    consumer_adapter = AIOKafkaConsumerAdapter(consumer)
    runner = KafkaWorkerRunner(
        consumer_adapter, worker, KafkaDeadLetterPublisher(producer)
    )
    runtime = WorkerRuntime(
        runner=runner,
        database=store,
        grpc_clients=grpc_channels,
        producer=producer,
        tracing_shutdown=shutdown_tracing,
    )
    runtime.health = WorkerHealth(
        kafka_check=consumer_adapter.connectivity_ready,
        database_check=store.database_ready,
        schema_check=store.schema_ready,
        provider_check=lambda: provider_configuration_ready(source),
        live_check=runtime.is_live,
    )
    start_http_server(config.metrics_port)
    return runtime


async def _run() -> None:
    runtime = await build_runtime()
    loop = asyncio.get_running_loop()
    for sig in (signal.SIGINT, signal.SIGTERM):
        loop.add_signal_handler(sig, lambda: asyncio.create_task(runtime.shutdown()))
    await runtime.run()


def main() -> None:
    asyncio.run(_run())


if __name__ == "__main__":
    main()
