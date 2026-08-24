package phase4e2e

import (
	"strings"
	"testing"

	"enjoythings/services/internal/event"
	"enjoythings/services/internal/saga"
)

const operatorID = "dddddddd-dddd-dddd-dddd-dddddddddddd"

func TestResumingAReviewAppliesTheDeferredPaymentResult(t *testing.T) {
	h := newHarness(t)
	started := h.startPayment(t, "99999999-9999-9999-9999-999999999999")
	h.scoreFraud(t, flagVerdict)
	h.deliverFraudVerdict(t)
	h.completePayment(t, started.PaymentID)
	h.requireSagaState(t, started.PaymentID, saga.StateFraudReview)

	resumed, err := h.orchestrator.ResumeFraudReview(h.ctx, saga.FraudReviewDecision{
		PaymentID: started.PaymentID,
		ActorID:   operatorID,
		Reason:    "manual check cleared",
		TraceID:   "trace-review-" + started.PaymentID,
	})
	if err != nil {
		t.Fatalf("saga boundary: ResumeFraudReview: %v", err)
	}

	if resumed.State != saga.StateCompleted {
		t.Fatalf("saga state = %s, want %s", resumed.State, saga.StateCompleted)
	}
	if resumed.DeferredPaymentJSON != "" {
		t.Fatalf("saga boundary: deferred payment = %q, want it applied and cleared", resumed.DeferredPaymentJSON)
	}
	if h.ledger.confirmCalls != 1 {
		t.Fatalf("ledger boundary: confirm calls = %d, want 1 after the resume", h.ledger.confirmCalls)
	}
	h.requirePublishedCount(t, saga.TopicTxCompleted, 1)

	h.deliverNotifications(t)
	h.requireNotificationSubjects(t, "Payment paused for review", "Payment completed")
}

func TestResumingAReviewWithNoResultWaitsForThePaymentProcessor(t *testing.T) {
	h := newHarness(t)
	started := h.startPayment(t, "9a9a9a9a-9a9a-9a9a-9a9a-9a9a9a9a9a9a")
	h.scoreFraud(t, flagVerdict)
	h.deliverFraudVerdict(t)

	resumed, err := h.orchestrator.ResumeFraudReview(h.ctx, saga.FraudReviewDecision{
		PaymentID: started.PaymentID,
		ActorID:   operatorID,
	})
	if err != nil {
		t.Fatalf("saga boundary: ResumeFraudReview: %v", err)
	}
	if resumed.State != saga.StatePaymentProcessing {
		t.Fatalf("saga state = %s, want %s", resumed.State, saga.StatePaymentProcessing)
	}
	h.requirePublishedCount(t, saga.TopicTxCompleted, 0)

	// The processor result arrives afterwards and takes the normal path.
	h.completePayment(t, started.PaymentID)

	h.requireSagaState(t, started.PaymentID, saga.StateCompleted)
	h.requirePublishedCount(t, saga.TopicTxCompleted, 1)
}

func TestRejectingAReviewRefundsThePayerAndFailsTheSaga(t *testing.T) {
	h := newHarness(t)
	started := h.startPayment(t, "9b9b9b9b-9b9b-9b9b-9b9b-9b9b9b9b9b9b")
	h.scoreFraud(t, blockVerdict)
	h.deliverFraudVerdict(t)

	rejected, err := h.orchestrator.RejectFraudReview(h.ctx, saga.FraudReviewDecision{
		PaymentID: started.PaymentID,
		ActorID:   operatorID,
		Reason:    "confirmed fraud",
	})
	if err != nil {
		t.Fatalf("saga boundary: RejectFraudReview: %v", err)
	}

	if rejected.State != saga.StateFailed || rejected.FailureCode != saga.FailureCodeFraudRejected {
		t.Fatalf("saga = %s/%s, want %s/%s", rejected.State, rejected.FailureCode, saga.StateFailed, saga.FailureCodeFraudRejected)
	}
	if h.ledger.cancelCalls != 1 {
		t.Fatalf("ledger boundary: cancel calls = %d, want 1", h.ledger.cancelCalls)
	}
	h.requirePublishedCount(t, saga.TopicTxFailed, 1)
	h.requirePartitionKey(t, saga.TopicTxFailed, fromWalletID)

	h.deliverNotifications(t)
	h.requireNotificationSubjects(t, "Payment paused for review", "Payment failed")
}

func TestReviewDecisionsAreAuditedWithTheDecidingOperator(t *testing.T) {
	h := newHarness(t)
	started := h.startPayment(t, "9c9c9c9c-9c9c-9c9c-9c9c-9c9c9c9c9c9c")
	h.scoreFraud(t, flagVerdict)
	h.deliverFraudVerdict(t)

	if _, err := h.orchestrator.RejectFraudReview(h.ctx, saga.FraudReviewDecision{
		PaymentID: started.PaymentID,
		ActorID:   operatorID,
		Reason:    "confirmed fraud",
	}); err != nil {
		t.Fatalf("saga boundary: RejectFraudReview: %v", err)
	}

	found := false
	for _, audit := range h.store.audits {
		if audit.Kind != saga.FraudAuditKindReviewRejected {
			continue
		}
		found = true
		if !strings.Contains(audit.DetailsJSON, operatorID) {
			t.Fatalf("saga audit boundary: details = %q, want the operator id", audit.DetailsJSON)
		}
		if audit.SagaState != saga.StateFraudReview {
			t.Fatalf("saga audit boundary: state = %q, want %s", audit.SagaState, saga.StateFraudReview)
		}
	}
	if !found {
		t.Fatalf("saga audit boundary: no %s record", saga.FraudAuditKindReviewRejected)
	}
}

func TestReviewDecisionsAreRejectedForSagasNotUnderReview(t *testing.T) {
	h := newHarness(t)
	started := h.startPayment(t, "9d9d9d9d-9d9d-9d9d-9d9d-9d9d9d9d9d9d")
	h.scoreFraud(t, allowVerdict)

	if _, err := h.orchestrator.ResumeFraudReview(h.ctx, saga.FraudReviewDecision{
		PaymentID: started.PaymentID,
		ActorID:   operatorID,
	}); err == nil {
		t.Fatal("saga boundary: an active payment was resumed out of review")
	}

	h.requireSagaState(t, started.PaymentID, saga.StatePaymentProcessing)
	h.requirePublishedCount(t, event.TxPausedTopic, 0)
}
