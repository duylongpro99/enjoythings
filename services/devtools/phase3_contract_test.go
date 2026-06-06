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

func TestPhase3SagaGatewayAndComposeWiring(t *testing.T) {
	root := repoRoot(t)

	gateway := readText(t, filepath.Join(root, "cmd", "gateway", "main.go"))
	for _, snippet := range []string{
		"NewSagaClient",
		"NewPayments(sagaClient)",
		`routes.Handle("/v1/payments/"`,
		"cfg.SagaGRPCAddr",
	} {
		requireContains(t, gateway, snippet)
	}

	compose := readText(t, filepath.Join(root, "docker-compose.yml"))
	for _, snippet := range []string{
		"saga-orchestrator:",
		"SAGA_GRPC_ADDR: saga-orchestrator:9093",
		"SAGA_CONSUMER_GROUP_ID: saga-orchestrator",
	} {
		requireContains(t, compose, snippet)
	}
}

func TestPhase3SmokeCommandCoversDeployedSagaBoundaries(t *testing.T) {
	root := repoRoot(t)

	smoke := readText(t, filepath.Join(root, "devtools", "phase3_smoke", "main.go"))
	for _, snippet := range []string{
		"/v1/verification/submit",
		"/v1/transfers",
		"/v1/payments/",
		"saga.TopicTxCompleted",
		"saga.TopicTxFailed",
		`"CONFIRMED"`,
		`"CANCELED"`,
		"attempt_count",
	} {
		requireContains(t, smoke, snippet)
	}
}

func TestPhase3WalletRolloutValidatorAndLocalCommands(t *testing.T) {
	root := repoRoot(t)

	script := readText(t, filepath.Join(root, "devtools", "k8s_wallet_rollout.sh"))
	for _, snippet := range []string{
		"kubectl scale deployment/wallet --replicas=2",
		"kubectl rollout restart deployment/wallet",
		"kubectl rollout status deployment/wallet",
		"curl -fsS",
		"probe_failures",
	} {
		requireContains(t, script, snippet)
	}

	makefile := readText(t, filepath.Join(root, "Makefile"))
	for _, snippet := range []string{
		"test-phase3-e2e:",
		"go test ./internal/phase3e2e -v",
		"phase3-smoke:",
		"go run ./devtools/phase3_smoke",
		"wallet-rollout-test:",
		"./devtools/k8s_wallet_rollout.sh",
	} {
		requireContains(t, makefile, snippet)
	}

	readme := readText(t, filepath.Join(root, "README.md"))
	requireContains(t, readme, "make test-phase3-e2e")
	requireContains(t, readme, "make phase3-smoke")
	requireContains(t, readme, "make wallet-rollout-test")

	guide := readText(t, filepath.Join(root, "docs", "phase3", "kubernetes-local-guide.md"))
	requireContains(t, guide, "make wallet-rollout-test")

	deployments := readText(t, filepath.Join(root, "charts", "enjoythings", "templates", "applications.yaml"))
	for _, snippet := range []string{
		"maxUnavailable: 0",
		"maxSurge: 1",
		"preStop:",
		"sleep 5",
		"terminationGracePeriodSeconds: 15",
	} {
		requireContains(t, deployments, snippet)
	}
}

func TestPhase3NotificationExecutableAndComposeWiring(t *testing.T) {
	root := repoRoot(t)

	notification := readText(t, filepath.Join(root, "cmd", "notification", "main.go"))
	for _, snippet := range []string{
		"LoadNotification",
		"NewStubEmailAdapter",
		"NewStubSMSAdapter",
		"NewKafkaConsumer",
	} {
		requireContains(t, notification, snippet)
	}

	compose := readText(t, filepath.Join(root, "docker-compose.yml"))
	for _, snippet := range []string{
		"--topic tx.completed",
		"--topic tx.failed",
		"notification:",
		"SERVICE: notification",
		"NOTIFICATION_CONSUMER_GROUP: notification-service",
	} {
		requireContains(t, compose, snippet)
	}
}
