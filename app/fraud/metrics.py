from __future__ import annotations

from prometheus_client import REGISTRY, CollectorRegistry, Counter, Histogram

RISK_BUCKETS = tuple(index / 10 for index in range(11))
MODEL_LATENCY_BUCKETS = (0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30)
SESSION_DURATION_BUCKETS = (0.25, 0.5, 1, 2.5, 5, 10, 30, 60)

_ACTIONS = {"allow", "flag", "block", "fail_open"}
_ENRICHMENT_METHODS = {"history", "velocity", "kyc"}
_OUTCOMES = {"success", "failure", "retry"}
_CALLBACKS = {"input_guard", "output_validator"}
_REJECTION_REASONS = {
    "prompt_empty",
    "uuid_detected",
    "sensitive_key_detected",
    "prompt_too_large",
    "response_too_large",
    "invalid_json",
    "invalid_schema",
    "invalid_action",
    "invalid_score",
    "sensitive_output",
}
_EVENT_TOPICS = {"fraud.flagged", "fraud.error"}


class FraudMetrics:
    def __init__(self, registry: CollectorRegistry = REGISTRY) -> None:
        self.registry = registry
        self._scored = Counter(
            "fraud_transactions_scored_total",
            "Fraud scoring outcomes.",
            ("action", "provider"),
            registry=registry,
        )
        self._model_latency = Histogram(
            "fraud_model_latency_seconds",
            "Fraud model call latency.",
            ("provider", "model"),
            buckets=MODEL_LATENCY_BUCKETS,
            registry=registry,
        )
        self._risk_score = Histogram(
            "fraud_risk_score",
            "Fraud risk score distribution.",
            buckets=RISK_BUCKETS,
            registry=registry,
        )
        self._enrichment = Counter(
            "fraud_enrichment_calls_total",
            "Fraud enrichment calls.",
            ("method", "outcome"),
            registry=registry,
        )
        self._rejections = Counter(
            "fraud_callback_rejections_total",
            "Fraud guard and validator rejections.",
            ("callback", "reason"),
            registry=registry,
        )
        self._session_duration = Histogram(
            "fraud_session_duration_seconds",
            "Fraud session duration.",
            ("outcome",),
            buckets=SESSION_DURATION_BUCKETS,
            registry=registry,
        )
        self._events = Counter(
            "fraud_events_published_total",
            "Fraud output events published.",
            ("topic", "outcome"),
            registry=registry,
        )

    def transaction_scored(self, action: str, provider: str) -> None:
        self._scored.labels(_enum(action, _ACTIONS), _configured(provider)).inc()

    def model_latency(self, provider: str, model: str, seconds: float) -> None:
        self._model_latency.labels(_configured(provider), _configured(model)).observe(seconds)

    def risk_score(self, score: float) -> None:
        self._risk_score.observe(max(0.0, min(1.0, score)))

    def enrichment_call(self, method: str, outcome: str) -> None:
        self._enrichment.labels(
            _enum(method, _ENRICHMENT_METHODS), _enum(outcome, _OUTCOMES)
        ).inc()

    def callback_rejection(self, callback: str, reason: str) -> None:
        self._rejections.labels(
            _enum(callback, _CALLBACKS), _enum(reason, _REJECTION_REASONS)
        ).inc()

    def session_duration(self, outcome: str, seconds: float) -> None:
        self._session_duration.labels(_enum(outcome, _ACTIONS | {"failure"})).observe(seconds)

    def event_published(self, topic: str, outcome: str) -> None:
        self._events.labels(
            _enum(topic, _EVENT_TOPICS), _enum(outcome, _OUTCOMES)
        ).inc()


def _enum(value: str, allowed: set[str]) -> str:
    if value not in allowed:
        raise ValueError(f"unbounded metric label: {value!r}")
    return value


def _configured(value: str) -> str:
    value = value.strip()
    if not value or len(value) > 64 or any(char not in "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._-" for char in value):
        raise ValueError("provider and model metric labels must be configured bounded IDs")
    return value


DEFAULT_METRICS = FraudMetrics()
