from opentelemetry.sdk.trace import TracerProvider
from opentelemetry.sdk.trace.export import SimpleSpanProcessor
from opentelemetry.sdk.trace.export.in_memory_span_exporter import InMemorySpanExporter

from app.fraud.tracing import (
    extract_kafka_context,
    inject_kafka_context,
    safe_attributes,
    trace_metadata,
)

KNOWN_TRACEPARENT = (
    "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
)


def test_kafka_context_round_trip_preserves_parent_child_relationship() -> None:
    exporter = InMemorySpanExporter()
    provider = TracerProvider()
    provider.add_span_processor(SimpleSpanProcessor(exporter))
    tracer = provider.get_tracer("test")

    parent = extract_kafka_context([("traceparent", KNOWN_TRACEPARENT.encode())])
    with tracer.start_as_current_span("fraud.worker.consume", context=parent) as consume:
        headers = inject_kafka_context([])
        published = extract_kafka_context(headers)
        with tracer.start_as_current_span("fraud.publish", context=published):
            pass

    spans = {span.name: span for span in exporter.get_finished_spans()}
    assert spans["fraud.worker.consume"].context.trace_id == int(
        "4bf92f3577b34da6a3ce929d0e0e4736", 16
    )
    assert spans["fraud.publish"].parent.span_id == consume.context.span_id


def test_safe_attributes_excludes_sensitive_values() -> None:
    attrs = safe_attributes(
        payment_id="payment-1",
        fraud_session_id="session-1",
        outcome="completed",
        user_id="private-user",
        wallet_id="private-wallet",
        prompt="private prompt",
        model_response="private response",
    )

    assert attrs == {
        "payment.id": "payment-1",
        "fraud.session_id": "session-1",
        "outcome": "completed",
    }
    assert "private" not in repr(attrs)


def test_grpc_trace_metadata_uses_active_w3c_context_not_payload_trace_id() -> None:
    exporter = InMemorySpanExporter()
    provider = TracerProvider()
    provider.add_span_processor(SimpleSpanProcessor(exporter))
    tracer = provider.get_tracer("test")

    parent = extract_kafka_context([("traceparent", KNOWN_TRACEPARENT.encode())])
    with tracer.start_as_current_span("fraud.grpc.enrichment", context=parent):
        metadata = dict(trace_metadata())

    assert metadata["traceparent"].startswith("00-4bf92f3577b34da6a3ce929d0e0e4736-")
    assert metadata["traceparent"] != "payload-trace-id"
