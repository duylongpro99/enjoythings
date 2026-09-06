import os
from collections.abc import Mapping
from dataclasses import dataclass


class FraudConfigError(ValueError):
    pass


_TRUE_VALUES = {"1", "true", "yes", "on"}
_FALSE_VALUES = {"0", "false", "no", "off", ""}


def _parse_bool(raw: str) -> bool:
    value = raw.strip().lower()
    if value in _TRUE_VALUES:
        return True
    if value in _FALSE_VALUES:
        return False
    raise ValueError(f"invalid boolean: {raw!r}")


@dataclass(frozen=True)
class FraudConfig:
    score_threshold: float = 0.75
    block_threshold: float = 0.90
    prompt_max_chars: int = 8000
    response_max_chars: int = 4000
    history_limit: int = 20
    consumer_group: str = "fraud-agent"
    request_topic: str = "fraud.score.requested"
    kafka_bootstrap_servers: str = "127.0.0.1:9092"
    metrics_port: int = 9101
    database_url: str = ""
    ledger_grpc_addr: str = "127.0.0.1:9091"
    verification_grpc_addr: str = "127.0.0.1:9094"
    grpc_tls_enabled: bool = False
    grpc_tls_cert_file: str = ""
    grpc_tls_key_file: str = ""
    grpc_tls_ca_file: str = ""
    # Days a completed fraud session is kept before the retention sweeper
    # deletes it. Zero, the default, keeps every row forever.
    audit_retention_days: int = 0
    sensitive_keys: tuple[str, ...] = (
        "user_id",
        "from_wallet_id",
        "to_wallet_id",
        "wallet_id",
    )

    @classmethod
    def from_env(cls, environ: Mapping[str, str] | None = None) -> "FraudConfig":
        source = environ if environ is not None else os.environ
        try:
            config = cls(
                score_threshold=float(source.get("FRAUD_SCORE_THRESHOLD", "0.75")),
                block_threshold=float(source.get("FRAUD_BLOCK_THRESHOLD", "0.90")),
                prompt_max_chars=int(source.get("FRAUD_PROMPT_MAX_CHARS", "8000")),
                response_max_chars=int(source.get("FRAUD_RESPONSE_MAX_CHARS", "4000")),
                history_limit=int(source.get("FRAUD_HISTORY_LIMIT", "20")),
                consumer_group=source.get("FRAUD_CONSUMER_GROUP", "fraud-agent").strip(),
                request_topic=source.get(
                    "FRAUD_REQUEST_TOPIC", "fraud.score.requested"
                ).strip(),
                kafka_bootstrap_servers=source.get(
                    "KAFKA_BOOTSTRAP_SERVERS", "127.0.0.1:9092"
                ).strip(),
                metrics_port=int(source.get("FRAUD_METRICS_PORT", "9101")),
                database_url=source.get("FRAUD_DATABASE_URL", "").strip(),
                ledger_grpc_addr=source.get(
                    "LEDGER_GRPC_ADDR", "127.0.0.1:9091"
                ).strip(),
                verification_grpc_addr=source.get(
                    "VERIFICATION_GRPC_ADDR", "127.0.0.1:9094"
                ).strip(),
                grpc_tls_enabled=_parse_bool(
                    source.get("FRAUD_GRPC_TLS_ENABLED", "false")
                ),
                grpc_tls_cert_file=source.get("FRAUD_GRPC_TLS_CERT_FILE", "").strip(),
                grpc_tls_key_file=source.get("FRAUD_GRPC_TLS_KEY_FILE", "").strip(),
                grpc_tls_ca_file=source.get("FRAUD_GRPC_TLS_CA_FILE", "").strip(),
                audit_retention_days=int(source.get("FRAUD_AUDIT_RETENTION_DAYS", "0")),
            )
        except ValueError as exc:
            raise FraudConfigError("fraud numeric settings must be valid numbers") from exc
        config.validate()
        return config

    def validate(self) -> None:
        if not 0.0 <= self.score_threshold < 1.0:
            raise FraudConfigError("FRAUD_SCORE_THRESHOLD must be in [0.0, 1.0)")
        if not 0.0 < self.block_threshold <= 1.0:
            raise FraudConfigError("FRAUD_BLOCK_THRESHOLD must be in (0.0, 1.0]")
        if self.score_threshold >= self.block_threshold:
            raise FraudConfigError("fraud score threshold must be lower than block threshold")
        if self.prompt_max_chars <= 0 or self.response_max_chars <= 0:
            raise FraudConfigError("fraud prompt and response limits must be positive")
        if not 1 <= self.history_limit <= 100:
            raise FraudConfigError("FRAUD_HISTORY_LIMIT must be from 1 through 100")
        if not self.consumer_group or not self.request_topic or not self.kafka_bootstrap_servers:
            raise FraudConfigError("fraud Kafka settings must be non-empty")
        if not 1 <= self.metrics_port <= 65535:
            raise FraudConfigError("FRAUD_METRICS_PORT must be a valid TCP port")
        if not self.database_url:
            raise FraudConfigError("FRAUD_DATABASE_URL is required")
        if not self.ledger_grpc_addr:
            raise FraudConfigError("LEDGER_GRPC_ADDR must be non-empty")
        if not self.verification_grpc_addr:
            raise FraudConfigError("VERIFICATION_GRPC_ADDR must be non-empty")
        if self.audit_retention_days < 0:
            raise FraudConfigError(
                "FRAUD_AUDIT_RETENTION_DAYS must be zero (keep forever) or positive"
            )
        if self.grpc_tls_enabled and not (
            self.grpc_tls_cert_file
            and self.grpc_tls_key_file
            and self.grpc_tls_ca_file
        ):
            raise FraudConfigError(
                "FRAUD_GRPC_TLS_CERT_FILE, FRAUD_GRPC_TLS_KEY_FILE, and "
                "FRAUD_GRPC_TLS_CA_FILE are required when FRAUD_GRPC_TLS_ENABLED is true"
            )
