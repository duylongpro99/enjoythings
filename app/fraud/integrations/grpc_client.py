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
    ledger_addr: str, verification_addr: str
) -> tuple[GrpcFraudDataClient, tuple[Any, Any]]:
    import grpc

    ledger_channel = grpc.insecure_channel(ledger_addr)
    verification_channel = grpc.insecure_channel(verification_addr)
    client = GrpcFraudDataClient(
        ledger_stub=ledger_pb2_grpc.LedgerServiceStub(ledger_channel),
        verification_stub=verification_pb2_grpc.VerificationServiceStub(
            verification_channel
        ),
    )
    return client, (ledger_channel, verification_channel)
