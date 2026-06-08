from prometheus_client import CollectorRegistry

from app.fraud.metrics import FraudMetrics


def test_fraud_metrics_use_bounded_labels_and_explicit_buckets() -> None:
    registry = CollectorRegistry()
    metrics = FraudMetrics(registry=registry)

    metrics.transaction_scored("flag", "fake-provider")
    metrics.model_latency("fake-provider", "fake-model", 0.3)
    metrics.risk_score(0.8)
    metrics.enrichment_call("history", "success")
    metrics.callback_rejection("input_guard", "uuid_detected")
    metrics.session_duration("flag", 1.5)
    metrics.event_published("fraud.flagged", "success")

    output = registry.collect()
    families = {family.name: family for family in output}
    assert set(families) >= {
        "fraud_transactions_scored",
        "fraud_model_latency_seconds",
        "fraud_risk_score",
        "fraud_enrichment_calls",
        "fraud_callback_rejections",
        "fraud_session_duration_seconds",
        "fraud_events_published",
    }
    rendered = "\n".join(
        repr(sample)
        for family in families.values()
        for sample in family.samples
    )
    assert "payment_id" not in rendered
    assert "session_id" not in rendered
    assert "0.1" in rendered
    assert "30.0" in rendered


def test_fraud_metrics_reject_unbounded_label_values() -> None:
    metrics = FraudMetrics(registry=CollectorRegistry())

    for call in (
        lambda: metrics.transaction_scored("payment-123", "provider"),
        lambda: metrics.enrichment_call("wallet-123", "success"),
        lambda: metrics.callback_rejection("input_guard", "exception text"),
        lambda: metrics.event_published("private.topic", "success"),
    ):
        try:
            call()
        except ValueError:
            pass
        else:
            raise AssertionError("unbounded metric label was accepted")
