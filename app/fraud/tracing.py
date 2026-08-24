from __future__ import annotations

import os
from collections.abc import Iterable, Mapping
from contextlib import contextmanager, suppress
from typing import Any

from opentelemetry import context as otel_context
from opentelemetry import propagate, trace
from opentelemetry.exporter.otlp.proto.http.trace_exporter import OTLPSpanExporter
from opentelemetry.propagate import set_global_textmap
from opentelemetry.sdk.resources import SERVICE_NAME, Resource
from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import BatchSpanProcessor
from opentelemetry.sdk.trace.sampling import ALWAYS_ON, ParentBased, TraceIdRatioBased
from opentelemetry.trace import Status, StatusCode
from opentelemetry.trace.propagation.tracecontext import TraceContextTextMapPropagator

TRACEPARENT_HEADER = "traceparent"
TRACESTATE_HEADER = "tracestate"

_ALLOWED_KEYS = {
    "service.name",
    "messaging.kafka.topic",
    "operation",
    "provider.id",
    "model.id",
    "fraud.session_id",
    "payment.id",
    "verdict.action",
    "outcome",
}


def init_tracing(service_name: str, environ: Mapping[str, str] | None = None):
    source = environ if environ is not None else os.environ
    set_global_textmap(TraceContextTextMapPropagator())
    provider = TracerProvider(
        resource=Resource.create({SERVICE_NAME: service_name}),
        sampler=_sampler(source),
    )
    if source.get("OTEL_EXPORTER_OTLP_ENDPOINT"):
        with suppress(Exception):
            provider.add_span_processor(BatchSpanProcessor(OTLPSpanExporter()))
    trace.set_tracer_provider(provider)
    return provider.shutdown


def tracer():
    return trace.get_tracer("app.fraud")


def extract_kafka_context(headers: Iterable[tuple[str, bytes | str]] | None):
    carrier: dict[str, str] = {}
    for key, value in headers or ():
        if key.lower() in (TRACEPARENT_HEADER, TRACESTATE_HEADER):
            carrier[key] = value.decode() if isinstance(value, bytes) else str(value)
    return propagate.extract(carrier)


def inject_kafka_context(headers: list[tuple[str, bytes]] | None = None) -> list[tuple[str, bytes]]:
    carrier: dict[str, str] = {}
    propagate.inject(carrier)
    result = list(headers or [])
    result = [(k, v) for k, v in result if k.lower() not in (TRACEPARENT_HEADER, TRACESTATE_HEADER)]
    for key in (TRACEPARENT_HEADER, TRACESTATE_HEADER):
        if carrier.get(key):
            result.append((key, carrier[key].encode()))
    return result


def trace_metadata() -> tuple[tuple[str, str], ...]:
    carrier: dict[str, str] = {}
    propagate.inject(carrier)
    return tuple((key, value) for key, value in carrier.items() if key in (TRACEPARENT_HEADER, TRACESTATE_HEADER))


def safe_attributes(**values: Any) -> dict[str, str]:
    attrs: dict[str, str] = {}
    mapping = {
        "service_name": "service.name",
        "topic": "messaging.kafka.topic",
        "operation": "operation",
        "provider_id": "provider.id",
        "model_id": "model.id",
        "fraud_session_id": "fraud.session_id",
        "payment_id": "payment.id",
        "verdict_action": "verdict.action",
        "outcome": "outcome",
    }
    for key, value in values.items():
        attr_key = mapping.get(key, key)
        if attr_key not in _ALLOWED_KEYS or value is None:
            continue
        attrs[attr_key] = str(value)[:128]
    return attrs


@contextmanager
def start_span(name: str, *, context=None, **attrs: Any):
    token = otel_context.attach(context) if context is not None else None
    try:
        with tracer().start_as_current_span(name, attributes=safe_attributes(**attrs)) as span:
            try:
                yield span
            except Exception as exc:
                record_error(span, exc)
                raise
    finally:
        if token is not None:
            otel_context.detach(token)


def record_error(span, exc: Exception) -> None:
    error_type = exc.__class__.__name__[:64]
    span.record_exception(Exception(error_type))
    span.set_status(Status(StatusCode.ERROR, error_type))


def _sampler(source: Mapping[str, str]):
    app_env = source.get("APP_ENV", "local").lower()
    if app_env in {"local", "dev"}:
        return ALWAYS_ON
    ratio = 0.1
    try:
        ratio = float(source.get("OTEL_TRACES_SAMPLER_ARG", "0.1"))
    except ValueError:
        ratio = 0.1
    return ParentBased(TraceIdRatioBased(min(max(ratio, 0.0), 1.0)))
