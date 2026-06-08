import ast
import importlib
from pathlib import Path
from types import SimpleNamespace

import pytest

from app.fraud.dto import KYCStatus, TransactionHistoryEntry, VelocityMetrics
from app.fraud.integrations.grpc_client import (
    EnrichmentError,
    GrpcFraudDataClient,
)


def test_grpc_client_maps_ledger_history_without_identifiers() -> None:
    ledger = FakeLedgerStub(
        history=[
            SimpleNamespace(
                direction="debit",
                amount_cents=1250,
                currency="USD",
                occurred_at=FakeTimestamp("2026-06-06T00:00:00+00:00"),
                wallet_id="must-not-leak",
                transfer_id="must-not-leak",
            )
        ]
    )
    client = GrpcFraudDataClient(ledger_stub=ledger, verification_stub=FakeVerificationStub())

    entries = client.get_transaction_history_sync("wallet-1", 20, "trace-1")

    assert entries == [
        TransactionHistoryEntry(
            direction="debit",
            amount_cents=1250,
            currency="USD",
            occurred_at=FakeTimestamp("2026-06-06T00:00:00+00:00").ToDatetime(),
        )
    ]
    assert not hasattr(entries[0], "wallet_id")
    assert ledger.history_request.wallet_id == "wallet-1"
    assert ledger.history_timeout == 2.0


def test_grpc_client_maps_velocity_and_kyc_status() -> None:
    ledger = FakeLedgerStub(
        velocity=SimpleNamespace(
            transactions_last_hour=2,
            amount_last_hour_cents=3000,
            average_amount_30d_cents=1000,
            distinct_recipients_30d=4,
        )
    )
    verification = FakeVerificationStub(status="verified")
    client = GrpcFraudDataClient(ledger_stub=ledger, verification_stub=verification)

    assert client.get_velocity_metrics_sync("wallet-1", "trace-1") == VelocityMetrics(
        transactions_last_hour=2,
        amount_last_hour_cents=3000,
        average_amount_30d_cents=1000,
        distinct_recipients_30d=4,
    )
    assert client.get_kyc_status_sync("user-1", "trace-1") == KYCStatus(status="verified")


@pytest.mark.parametrize("code", ["UNAVAILABLE", "DEADLINE_EXCEEDED", "INTERNAL", "UNKNOWN"])
def test_grpc_client_classifies_infrastructure_failures_as_retryable(code: str) -> None:
    retrying = GrpcFraudDataClient(
        ledger_stub=FakeLedgerStub(error=FakeRpcError(code)),
        verification_stub=FakeVerificationStub(),
    )
    with pytest.raises(EnrichmentError) as retryable:
        retrying.get_velocity_metrics_sync("wallet-1", "trace-1")
    assert retryable.value.retryable is True


@pytest.mark.parametrize("code", ["INVALID_ARGUMENT", "NOT_FOUND"])
def test_grpc_client_classifies_ledger_fact_failures_as_non_retryable(code: str) -> None:
    invalid = GrpcFraudDataClient(
        ledger_stub=FakeLedgerStub(error=FakeRpcError(code)),
        verification_stub=FakeVerificationStub(),
    )
    with pytest.raises(EnrichmentError) as non_retryable:
        invalid.get_velocity_metrics_sync("wallet-1", "trace-1")
    assert non_retryable.value.retryable is False


def test_grpc_client_retries_unavailable_once_then_succeeds() -> None:
    ledger = FakeLedgerStub(error_sequence=[FakeRpcError("UNAVAILABLE"), None])
    client = GrpcFraudDataClient(ledger_stub=ledger, verification_stub=FakeVerificationStub())

    metrics = client.get_velocity_metrics_sync("wallet-1", "trace-1")

    assert metrics == VelocityMetrics()
    assert ledger.velocity_calls == 2


def test_grpc_client_retries_kyc_unavailable_once_then_succeeds(monkeypatch) -> None:
    delays: list[float] = []
    monkeypatch.setattr("app.fraud.integrations.grpc_client.time.sleep", delays.append)
    verification = FakeVerificationStub(
        error_sequence=[FakeRpcError("UNAVAILABLE"), None], status="verified"
    )
    client = GrpcFraudDataClient(ledger_stub=FakeLedgerStub(), verification_stub=verification)

    assert client.get_kyc_status_sync("user-1", "trace-1") == KYCStatus(status="verified")
    assert verification.calls == 2
    assert delays == [0.1]


def test_grpc_client_maps_missing_kyc_to_unverified_without_retry() -> None:
    verification = FakeVerificationStub(error=FakeRpcError("NOT_FOUND"))
    client = GrpcFraudDataClient(ledger_stub=FakeLedgerStub(), verification_stub=verification)

    assert client.get_kyc_status_sync("user-1", "trace-1") == KYCStatus(
        status="unverified"
    )
    assert verification.calls == 1


@pytest.mark.parametrize("code", ["INVALID_ARGUMENT", "NOT_FOUND", "DEADLINE_EXCEEDED"])
def test_grpc_client_does_not_retry_non_unavailable_ledger_errors(code: str) -> None:
    ledger = FakeLedgerStub(error=FakeRpcError(code))
    client = GrpcFraudDataClient(ledger_stub=ledger, verification_stub=FakeVerificationStub())

    with pytest.raises(EnrichmentError):
        client.get_velocity_metrics_sync("wallet-1", "trace-1")

    assert ledger.velocity_calls == 1


def test_grpc_client_sets_deadline_and_trace_metadata_on_every_rpc() -> None:
    ledger = FakeLedgerStub()
    verification = FakeVerificationStub()
    client = GrpcFraudDataClient(ledger_stub=ledger, verification_stub=verification)

    client.get_transaction_history_sync("wallet-1", 20, "trace-1")
    client.get_velocity_metrics_sync("wallet-1", "trace-1")
    client.get_kyc_status_sync("user-1", "trace-1")

    assert ledger.calls == [
        ("history", 2.0, ()),
        ("velocity", 2.0, ()),
    ]
    assert verification.call_options == [(2.0, ())]


@pytest.mark.parametrize(
    "call",
    [
        lambda client: client.get_transaction_history_sync("", 20, "trace-1"),
        lambda client: client.get_transaction_history_sync("wallet-1", 0, "trace-1"),
        lambda client: client.get_transaction_history_sync("wallet-1", 101, "trace-1"),
        lambda client: client.get_velocity_metrics_sync("", "trace-1"),
        lambda client: client.get_kyc_status_sync("", "trace-1"),
    ],
)
def test_grpc_client_rejects_invalid_private_identifiers_without_rpc(call) -> None:
    ledger = FakeLedgerStub()
    verification = FakeVerificationStub()
    client = GrpcFraudDataClient(ledger_stub=ledger, verification_stub=verification)

    with pytest.raises(EnrichmentError) as raised:
        call(client)

    assert raised.value.retryable is False
    assert ledger.calls == []
    assert verification.calls == 0


def test_generated_protobuf_imports_stay_in_grpc_client_module() -> None:
    offenders: list[str] = []
    for path in Path("app/fraud").rglob("*.py"):
        if (
            path.as_posix() == "app/fraud/integrations/grpc_client.py"
            or "app/fraud/integrations/gen/" in path.as_posix()
        ):
            continue
        tree = ast.parse(path.read_text())
        for node in ast.walk(tree):
            if isinstance(node, ast.Import):
                names = [alias.name for alias in node.names]
            elif isinstance(node, ast.ImportFrom):
                names = [node.module or ""]
            else:
                continue
            if any(
                "app.fraud.integrations.gen" in name
                or name == "grpc"
                or name.endswith(("_pb2", "_pb2_grpc"))
                for name in names
            ):
                offenders.append(path.as_posix())
    assert offenders == []


def test_committed_generated_grpc_clients_are_importable() -> None:
    modules = (
        "app.fraud.integrations.gen.ledger.v1.ledger_pb2",
        "app.fraud.integrations.gen.ledger.v1.ledger_pb2_grpc",
        "app.fraud.integrations.gen.verification.v1.verification_pb2",
        "app.fraud.integrations.gen.verification.v1.verification_pb2_grpc",
    )
    for module in modules:
        assert importlib.import_module(module)


class FakeTimestamp:
    def __init__(self, value: str) -> None:
        from datetime import datetime

        self.value = datetime.fromisoformat(value)

    def ToDatetime(self):
        return self.value


class FakeRpcError(Exception):
    def __init__(self, code_name: str) -> None:
        self.code_name = code_name

    def code(self):
        return SimpleNamespace(name=self.code_name)


class FakeLedgerStub:
    def __init__(self, history=None, velocity=None, error=None, error_sequence=None) -> None:
        self.history = history or []
        self.velocity = velocity or SimpleNamespace(
            transactions_last_hour=0,
            amount_last_hour_cents=0,
            average_amount_30d_cents=0,
            distinct_recipients_30d=0,
        )
        self.error = error
        self.error_sequence = list(error_sequence or [])
        self.history_request = None
        self.history_timeout = None
        self.velocity_calls = 0
        self.calls = []

    def GetFraudTransactionHistory(self, request, timeout=None, metadata=None):
        self.calls.append(("history", timeout, metadata))
        self.history_request = request
        self.history_timeout = timeout
        if self.error:
            raise self.error
        return SimpleNamespace(entries=self.history)

    def GetFraudVelocityMetrics(self, request, timeout=None, metadata=None):
        self.calls.append(("velocity", timeout, metadata))
        self.velocity_calls += 1
        if self.error_sequence:
            next_error = self.error_sequence.pop(0)
            if next_error is not None:
                raise next_error
        if self.error:
            raise self.error
        return self.velocity


class FakeVerificationStub:
    def __init__(self, status: str = "unverified", error=None, error_sequence=None) -> None:
        self.status = status
        self.error = error
        self.error_sequence = list(error_sequence or [])
        self.calls = 0
        self.call_options = []

    def GetStatus(self, request, timeout=None, metadata=None):
        self.calls += 1
        self.call_options.append((timeout, metadata))
        if self.error_sequence:
            next_error = self.error_sequence.pop(0)
            if next_error is not None:
                raise next_error
        if self.error:
            raise self.error
        return SimpleNamespace(status=self.status)
