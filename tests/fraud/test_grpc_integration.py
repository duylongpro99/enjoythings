from concurrent.futures import ThreadPoolExecutor
from datetime import datetime, timezone

import grpc
from google.protobuf.timestamp_pb2 import Timestamp

from app.fraud.dto import KYCStatus, TransactionHistoryEntry, VelocityMetrics
from app.fraud.integrations.gen.ledger.v1 import ledger_pb2, ledger_pb2_grpc
from app.fraud.integrations.gen.verification.v1 import (
    verification_pb2,
    verification_pb2_grpc,
)
from app.fraud.integrations.grpc_client import GrpcFraudDataClient


def test_grpc_fraud_client_calls_ledger_and_verification_without_model() -> None:
    occurred_at = datetime(2026, 6, 7, 12, 0, tzinfo=timezone.utc)
    timestamp = Timestamp()
    timestamp.FromDatetime(occurred_at)
    ledger = LedgerServicer(timestamp)
    verification = VerificationServicer()
    server = grpc.server(ThreadPoolExecutor(max_workers=2))
    ledger_pb2_grpc.add_LedgerServiceServicer_to_server(ledger, server)
    verification_pb2_grpc.add_VerificationServiceServicer_to_server(
        verification, server
    )
    port = server.add_insecure_port("127.0.0.1:0")
    server.start()

    channel = grpc.insecure_channel(f"127.0.0.1:{port}")
    try:
        client = GrpcFraudDataClient(
            ledger_stub=ledger_pb2_grpc.LedgerServiceStub(channel),
            verification_stub=verification_pb2_grpc.VerificationServiceStub(channel),
        )

        assert client.get_transaction_history_sync("wallet-private", 20, "trace-1") == [
            TransactionHistoryEntry(
                direction="debit",
                amount_cents=1250,
                currency="USD",
                occurred_at=occurred_at.replace(tzinfo=None),
            )
        ]
        assert client.get_velocity_metrics_sync(
            "wallet-private", "trace-1"
        ) == VelocityMetrics(
            transactions_last_hour=2,
            amount_last_hour_cents=2500,
            average_amount_30d_cents=900,
            distinct_recipients_30d=3,
        )
        assert client.get_kyc_status_sync("user-private", "trace-1") == KYCStatus(
            status="verified"
        )
    finally:
        channel.close()
        server.stop(grace=None).wait()

    assert ledger.history_request.wallet_id == "wallet-private"
    assert ledger.velocity_request.wallet_id == "wallet-private"
    assert verification.request.user_id == "user-private"


class LedgerServicer(ledger_pb2_grpc.LedgerServiceServicer):
    def __init__(self, occurred_at: Timestamp) -> None:
        self.occurred_at = occurred_at
        self.history_request = None
        self.velocity_request = None

    def GetFraudTransactionHistory(self, request, context):
        self.history_request = request
        return ledger_pb2.GetFraudTransactionHistoryResponse(
            entries=[
                ledger_pb2.FraudTransactionHistoryEntry(
                    direction="debit",
                    amount_cents=1250,
                    currency="USD",
                    occurred_at=self.occurred_at,
                )
            ]
        )

    def GetFraudVelocityMetrics(self, request, context):
        self.velocity_request = request
        return ledger_pb2.GetFraudVelocityMetricsResponse(
            transactions_last_hour=2,
            amount_last_hour_cents=2500,
            average_amount_30d_cents=900,
            distinct_recipients_30d=3,
        )


class VerificationServicer(verification_pb2_grpc.VerificationServiceServicer):
    def __init__(self) -> None:
        self.request = None

    def GetStatus(self, request, context):
        self.request = request
        return verification_pb2.GetStatusResponse(status="verified")
