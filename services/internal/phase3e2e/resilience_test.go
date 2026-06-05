package phase3e2e

import (
	"errors"
	"testing"

	"enjoythings/services/internal/paymentprocessor"
	"enjoythings/services/internal/saga"
)

func TestHappyPathPaymentCrossesAllServiceBoundaries(t *testing.T) {
	h := newHarness(t, railSuccess)
	h.verifyUser(t)

	started := h.startPayment(t, "11111111-1111-1111-1111-111111111111")
	h.drainEvents(t)

	h.requireSagaState(t, started.PaymentID, saga.StateCompleted)
	h.requireWalletBalance(t, 3750)
	h.requireLedgerStatus(t, "CONFIRMED")
	h.requirePublished(t, saga.TopicTxCompleted)
	h.requireNotification(t, "Payment completed")
}

func TestPaymentProcessorTerminalFailureCompensatesSaga(t *testing.T) {
	h := newHarness(t, railTerminalFailure)
	h.verifyUser(t)

	started := h.startPayment(t, "22222222-2222-2222-2222-222222222222")
	h.drainEvents(t)

	h.requireSagaState(t, started.PaymentID, saga.StateFailed)
	h.requireWalletBalance(t, 5000)
	h.requireLedgerStatus(t, "CANCELLED")
	h.requirePublished(t, saga.TopicTxFailed)
	h.requireNotification(t, "Payment failed")
}

func TestOrchestratorRestartAfterWalletDebitDoesNotDebitTwice(t *testing.T) {
	h := newHarness(t, railSuccess)
	h.verifyUser(t)
	h.failNextLedgerReserve()

	err := h.startPaymentError("33333333-3333-3333-3333-333333333333")
	if err == nil {
		t.Fatal("expected ledger boundary failure before restart")
	}
	h.restartOrchestrator(t)
	h.drainEvents(t)

	h.requireWalletDebitCalls(t, 1)
	h.requireSagaState(t, "33333333-3333-3333-3333-333333333333", saga.StateCompleted)
}

func TestDuplicatePaymentExecuteDoesNotChargeTwice(t *testing.T) {
	h := newHarness(t, railSuccess)
	h.verifyUser(t)
	h.startPayment(t, "44444444-4444-4444-4444-444444444444")

	h.deliverNextTopicTwice(t, saga.TopicPaymentExecute)
	h.drainEvents(t)

	h.requireRailCalls(t, 1)
	h.requireSagaState(t, "44444444-4444-4444-4444-444444444444", saga.StateCompleted)
}

func TestDuplicatePaymentCompletedKeepsSagaConfirmationIdempotent(t *testing.T) {
	h := newHarness(t, railSuccess)
	h.verifyUser(t)
	h.startPayment(t, "55555555-5555-5555-5555-555555555555")
	h.deliverNextTopic(t, saga.TopicPaymentExecute)

	h.deliverNextTopicTwice(t, saga.TopicPaymentCompleted)
	h.drainEvents(t)

	h.requireLedgerConfirmCalls(t, 1)
	h.requireSagaState(t, "55555555-5555-5555-5555-555555555555", saga.StateCompleted)
}

func TestUnverifiedUserReturnsFailedPreconditionAndGateway422(t *testing.T) {
	h := newHarness(t, railSuccess)

	err := h.startPaymentError("66666666-6666-6666-6666-666666666666")
	if !errors.Is(err, saga.ErrUnverified) {
		t.Fatalf("saga error = %v, want %v", err, saga.ErrUnverified)
	}
	h.requireGatewayUnverifiedStatus(t, 422)
	h.requireWalletDebitCalls(t, 0)
}

func TestVerificationAutoModePublishesAndNotifies(t *testing.T) {
	h := newHarness(t, railSuccess)

	record := h.verifyUser(t)
	h.drainEvents(t)

	if record.Status != "verified" {
		t.Fatalf("verification status = %s, want verified", record.Status)
	}
	h.requirePublished(t, "user.verified")
	h.requireNotification(t, "Verification approved")
}

func TestPaymentProcessorRetriesAtBoundaryThenCompletes(t *testing.T) {
	h := newHarness(t, railRetryOnce)
	h.verifyUser(t)
	h.startPayment(t, "77777777-7777-7777-7777-777777777777")
	h.drainEvents(t)

	h.requireRailCalls(t, 2)
	h.requirePaymentAttempt(t, paymentprocessor.StatusCompleted, 2)
	h.requireSagaState(t, "77777777-7777-7777-7777-777777777777", saga.StateCompleted)
}
