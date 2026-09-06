import asyncio
import time
from typing import Any

from app.fraud.dto import KYCStatus, TransactionHistoryEntry, VelocityMetrics
from app.fraud.integrations.gen.ledger.v1 import ledger_pb2, ledger_pb2_grpc
from app.fraud.integrations.gen.verification.v1 import verification_pb2, verification_pb2_grpc
from app.fraud.ports import FraudDataPort
from app.fraud.tracing import start_span, trace_metadata

RPC_TIMEOUT_SECONDS = 2.0
UNAVAILABLE_RETRY_DELAY_SECONDS = 0.1


class EnrichmentError(RuntimeError):
    def __init__(self, reason: str, *, retryable: bool) -> None:
        super().__init__(reason)
        self.retryable = retryable


class GrpcFraudDataClient(FraudDataPort):
    def __init__(self, *, ledger_stub: Any, verification_stub: Any) -> None:
        self._ledger = ledger_stub
        self._verification = verification_stub

    async def get_transaction_history(
        self, wallet_id: str, limit: int, trace_id: str
    ) -> list[TransactionHistoryEntry]:
        return await asyncio.to_thread(
            self.get_transaction_history_sync, wallet_id, limit, trace_id
        )

    async def get_velocity_metrics(self, wallet_id: str, trace_id: str) -> VelocityMetrics:
        return await asyncio.to_thread(
            self.get_velocity_metrics_sync, wallet_id, trace_id
        )

    async def get_kyc_status(self, user_id: str, trace_id: str) -> KYCStatus:
        return await asyncio.to_thread(self.get_kyc_status_sync, user_id, trace_id)

    def get_transaction_history_sync(
        self, wallet_id: str, limit: int, trace_id: str
    ) -> list[TransactionHistoryEntry]:
        if not wallet_id or limit < 1 or limit > 100:
            raise EnrichmentError("invalid_identifier", retryable=False)
        response = self._call_with_retry(
            self._ledger.GetFraudTransactionHistory,
            ledger_pb2.GetFraudTransactionHistoryRequest(
                wallet_id=wallet_id, limit=limit, trace_id=trace_id
            ),
            trace_id,
        )
        return [
            TransactionHistoryEntry(
                direction=str(entry.direction),
                amount_cents=int(entry.amount_cents),
                currency=str(entry.currency),
                occurred_at=entry.occurred_at.ToDatetime(),
            )
            for entry in response.entries
        ]

    def get_velocity_metrics_sync(self, wallet_id: str, trace_id: str) -> VelocityMetrics:
        if not wallet_id:
            raise EnrichmentError("invalid_identifier", retryable=False)
        response = self._call_with_retry(
            self._ledger.GetFraudVelocityMetrics,
            ledger_pb2.GetFraudVelocityMetricsRequest(
                wallet_id=wallet_id, trace_id=trace_id
            ),
            trace_id,
        )
        return VelocityMetrics(
            transactions_last_hour=int(response.transactions_last_hour),
            amount_last_hour_cents=int(response.amount_last_hour_cents),
            average_amount_30d_cents=int(response.average_amount_30d_cents),
            distinct_recipients_30d=int(response.distinct_recipients_30d),
        )

    def get_kyc_status_sync(self, user_id: str, trace_id: str) -> KYCStatus:
        if not user_id:
            raise EnrichmentError("invalid_identifier", retryable=False)
        response = self._call_with_retry(
            self._verification.GetStatus,
            verification_pb2.GetStatusRequest(user_id=user_id, trace_id=trace_id),
            trace_id,
            not_found_is_none=True,
        )
        if response is None:
            return KYCStatus(status="unverified")
        return KYCStatus(status=str(response.status or "unverified"))

    def _call_with_retry(
        self, method, request, trace_id: str, *, not_found_is_none: bool = False
    ):
        rpc_name = getattr(method, "__name__", "grpc_call")
        try:
            with start_span(f"fraud.grpc.{rpc_name}", operation="grpc"):
                return method(
                    request,
                    timeout=RPC_TIMEOUT_SECONDS,
                    metadata=_trace_metadata(trace_id),
                )
        except Exception as exc:
            if not_found_is_none and _rpc_code_name(exc) == "NOT_FOUND":
                return None
            if _rpc_code_name(exc) != "UNAVAILABLE":
                raise _enrichment_error(exc) from exc
            time.sleep(UNAVAILABLE_RETRY_DELAY_SECONDS)
        try:
            with start_span(f"fraud.grpc.{rpc_name}", operation="grpc"):
                return method(
                    request,
                    timeout=RPC_TIMEOUT_SECONDS,
                    metadata=_trace_metadata(trace_id),
                )
        except Exception as exc:
            if not_found_is_none and _rpc_code_name(exc) == "NOT_FOUND":
                return None
            raise _enrichment_error(exc) from exc


def _trace_metadata(trace_id: str) -> tuple[tuple[str, str], ...]:
    return trace_metadata()


def _rpc_code_name(exc: Exception) -> str:
    code = getattr(exc, "code", None)
    if not callable(code):
        return ""
    value = code()
    return str(getattr(value, "name", value))


def _enrichment_error(exc: Exception) -> EnrichmentError:
    code = _rpc_code_name(exc)
    if code in {"INVALID_ARGUMENT", "NOT_FOUND"}:
        return EnrichmentError("non_retryable_enrichment_failed", retryable=False)
    if code in {"UNAVAILABLE", "DEADLINE_EXCEEDED", "INTERNAL", "UNKNOWN"}:
        return EnrichmentError("retryable_enrichment_failed", retryable=True)
    return EnrichmentError("retryable_enrichment_failed", retryable=True)


def build_grpc_fraud_data_client(
    ledger_addr: str,
    verification_addr: str,
    *,
    tls_enabled: bool = False,
    cert_file: str = "",
    key_file: str = "",
    ca_file: str = "",
) -> tuple[GrpcFraudDataClient, tuple[Any, Any]]:
    import grpc

    if tls_enabled:
        credentials = _channel_credentials(cert_file, key_file, ca_file)
        ledger_channel = grpc.secure_channel(ledger_addr, credentials)
        verification_channel = grpc.secure_channel(verification_addr, credentials)
    else:
        ledger_channel = grpc.insecure_channel(ledger_addr)
        verification_channel = grpc.insecure_channel(verification_addr)
    client = GrpcFraudDataClient(
        ledger_stub=ledger_pb2_grpc.LedgerServiceStub(ledger_channel),
        verification_stub=verification_pb2_grpc.VerificationServiceStub(
            verification_channel
        ),
    )
    return client, (ledger_channel, verification_channel)


def _channel_credentials(cert_file: str, key_file: str, ca_file: str) -> Any:
    """Build mutual-TLS channel credentials: the worker presents its own leaf
    certificate and verifies the server against the shared CA. The three paths
    are required together, matching the Go services' contract.

    The files are read once, when the channel is created. grpc-python has no
    client-side reload hook (only servers get dynamic credentials), so unlike
    the Go services a renewed certificate takes effect on the next process
    start; see services/docs/design-notes/phase5-cert-rotation.md."""
    import grpc

    if not (cert_file and key_file and ca_file):
        raise ValueError(
            "fraud gRPC TLS requires cert, key, and CA files when enabled"
        )
    with open(ca_file, "rb") as handle:
        root_certificates = handle.read()
    with open(cert_file, "rb") as handle:
        certificate_chain = handle.read()
    with open(key_file, "rb") as handle:
        private_key = handle.read()
    return grpc.ssl_channel_credentials(
        root_certificates=root_certificates,
        private_key=private_key,
        certificate_chain=certificate_chain,
    )
