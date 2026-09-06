package saga

import (
	"context"
	"testing"
	"time"
)

func TestExpireFraudReviewsRejectsOverdueReviews(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	wallet := &fakeWallet{}
	outbox := &fakeOutbox{}
	orchestrator, req := sagaUnderReview(ctx, t, store, wallet, &fakeLedger{}, outbox)
	orchestrator.clock = clockAt{fixedTime.Add(24*time.Hour + time.Minute)}

	expired, err := orchestrator.ExpireFraudReviews(ctx, 24*time.Hour)
	if err != nil {
		t.Fatalf("ExpireFraudReviews: %v", err)
	}
	if expired != 1 {
		t.Fatalf("expired = %d, want 1", expired)
	}
	current, _ := store.GetByPaymentID(ctx, req.PaymentID)
	if current.State != StateFailed || current.FailureCode != FailureCodeFraudRejected {
		t.Fatalf("saga = %s/%s, want %s with %s", current.State, current.FailureCode, StateFailed, FailureCodeFraudRejected)
	}
	if wallet.compensatedDebitID == "" {
		t.Fatal("wallet debit was not compensated")
	}
	if !outboxHasTopic(outbox, TopicTxFailed) {
		t.Fatalf("outbox topics = %v, want %s", outboxTopics(outbox), TopicTxFailed)
	}
	assertReviewAudit(t, store, FraudAuditKindReviewRejected, ReviewReaperActorID, "fraud review deadline of 24h0m0s exceeded")
}

func TestExpireFraudReviewsLeavesReviewsWithinTheDeadline(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	orchestrator, req := sagaUnderReview(ctx, t, store, &fakeWallet{}, &fakeLedger{}, &fakeOutbox{})
	orchestrator.clock = clockAt{fixedTime.Add(23 * time.Hour)}

	expired, err := orchestrator.ExpireFraudReviews(ctx, 24*time.Hour)
	if err != nil {
		t.Fatalf("ExpireFraudReviews: %v", err)
	}
	if expired != 0 {
		t.Fatalf("expired = %d, want 0", expired)
	}
	current, _ := store.GetByPaymentID(ctx, req.PaymentID)
	if current.State != StateFraudReview {
		t.Fatalf("state = %s, want it still %s", current.State, StateFraudReview)
	}
}

func TestExpireFraudReviewsIgnoresSagasOutsideReview(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	orchestrator := NewOrchestrator(store, &fakeVerification{status: VerificationVerified}, &fakeWallet{}, &fakeLedger{}, clockAt{fixedTime.Add(48 * time.Hour)})
	orchestrator.SetOutbox(&fakeOutbox{})
	req := startRequest()
	if _, err := orchestrator.StartPaymentSaga(ctx, req); err != nil {
		t.Fatalf("StartPaymentSaga: %v", err)
	}

	expired, err := orchestrator.ExpireFraudReviews(ctx, 24*time.Hour)
	if err != nil || expired != 0 {
		t.Fatalf("ExpireFraudReviews = %d, %v; want 0, nil", expired, err)
	}
	current, _ := store.GetByPaymentID(ctx, req.PaymentID)
	if current.State != StatePaymentProcessing {
		t.Fatalf("state = %s, want it untouched at %s", current.State, StatePaymentProcessing)
	}
}

func TestExpireFraudReviewsRequiresAPositiveTTL(t *testing.T) {
	orchestrator := NewOrchestrator(newMemoryStore(), &fakeVerification{}, &fakeWallet{}, &fakeLedger{}, fixedClock{})
	if _, err := orchestrator.ExpireFraudReviews(context.Background(), 0); err == nil {
		t.Fatal("ExpireFraudReviews accepted a zero ttl")
	}
}

type clockAt struct {
	at time.Time
}

func (clock clockAt) Now() time.Time {
	return clock.at
}
