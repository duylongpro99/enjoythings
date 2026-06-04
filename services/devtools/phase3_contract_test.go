package devtools

import (
	"path/filepath"
	"testing"
)

func TestPhase3ContractsDefineRequiredGrpcServices(t *testing.T) {
	root := repoRoot(t)

	required := map[string][]string{
		filepath.Join("proto", "saga", "v1", "saga.proto"): {
			"service SagaService",
			"rpc StartPaymentSaga(StartPaymentSagaRequest) returns (StartPaymentSagaResponse);",
			"rpc GetPaymentSaga(GetPaymentSagaRequest) returns (GetPaymentSagaResponse);",
			"string payment_id = 1;",
			"string idempotency_key = 2;",
			"string trace_id = 3;",
		},
		filepath.Join("proto", "wallet", "v1", "wallet.proto"): {
			"rpc DebitForSaga(DebitForSagaRequest) returns (DebitForSagaResponse);",
			"rpc CompensateDebit(CompensateDebitRequest) returns (CompensateDebitResponse);",
			"message DebitForSagaRequest",
			"message CompensateDebitRequest",
			"string payment_id = 1;",
			"string idempotency_key = 2;",
			"string trace_id = 3;",
		},
		filepath.Join("proto", "ledger", "v1", "ledger.proto"): {
			"rpc ReserveTransfer(ReserveTransferRequest) returns (ReserveTransferResponse);",
			"rpc ConfirmTransfer(ConfirmTransferRequest) returns (ConfirmTransferResponse);",
			"rpc CancelReservation(CancelReservationRequest) returns (CancelReservationResponse);",
			"message ReserveTransferRequest",
			"message ConfirmTransferRequest",
			"message CancelReservationRequest",
			"string payment_id = 1;",
			"string idempotency_key = 2;",
			"string trace_id = 3;",
		},
		filepath.Join("proto", "verification", "v1", "verification.proto"): {
			"service VerificationService",
			"rpc SubmitVerification(SubmitVerificationRequest) returns (SubmitVerificationResponse);",
			"rpc GetStatus(GetStatusRequest) returns (GetStatusResponse);",
			"string idempotency_key = 2;",
			"string trace_id = 3;",
		},
		filepath.Join("proto", "notification", "v1", "notification.proto"): {
			"service NotificationService",
			"rpc Health(HealthRequest) returns (HealthResponse);",
			"rpc PauseDelivery(PauseDeliveryRequest) returns (PauseDeliveryResponse);",
			"rpc ResumeDelivery(ResumeDeliveryRequest) returns (ResumeDeliveryResponse);",
		},
	}

	for relPath, snippets := range required {
		t.Run(relPath, func(t *testing.T) {
			contract := readText(t, filepath.Join(root, relPath))
			for _, snippet := range snippets {
				requireContains(t, contract, snippet)
			}
		})
	}
}

func TestPhase3ContractSpecDocumentsRoutesTopicsAndOperationalSemantics(t *testing.T) {
	root := repoRoot(t)

	spec := readText(t, filepath.Join(root, "docs", "phase3", "specs", "01-contracts-and-topics.md"))

	for _, snippet := range []string{
		"POST /v1/transfers",
		"GET /v1/payments/{payment_id}",
		"POST /v1/verification/submit",
		"GET /v1/verification/status",
		"202 Accepted",
		"payment.execute",
		"payment.completed",
		"payment.failed",
		"tx.completed",
		"tx.failed",
		"user.verified",
		"user.rejected",
		"Payload fields",
		"Partition key",
		"Consumer group",
		"Duplicate command with same idempotency key and same payload",
		"Conflicting command with same idempotency key and different payload",
		"trace_id",
		"X-Trace-Id",
		"gRPC metadata",
		"Kafka headers",
		"FAILED_PRECONDITION",
		"HTTP `422`",
	} {
		requireContains(t, spec, snippet)
	}
}

func TestPhase3PaymentProcessorExecutableAndComposeWiring(t *testing.T) {
	root := repoRoot(t)

	paymentProcessor := readText(t, filepath.Join(root, "cmd", "payment-processor", "main.go"))
	for _, snippet := range []string{
		"LoadPaymentProcessor",
		"NewHTTPRail",
		"NewKafkaConsumer",
		"NewPublisher",
	} {
		requireContains(t, paymentProcessor, snippet)
	}

	stubRail := readText(t, filepath.Join(root, "cmd", "stub-payment-rail", "main.go"))
	requireContains(t, stubRail, "NewStubRailHandler")

	compose := readText(t, filepath.Join(root, "docker-compose.yml"))
	for _, snippet := range []string{
		"--topic payment.execute",
		"--topic payment.completed",
		"--topic payment.failed",
		"payment-processor:",
		"stub-payment-rail:",
		"PAYMENT_RAIL_URL: http://stub-payment-rail:18090",
	} {
		requireContains(t, compose, snippet)
	}
}
