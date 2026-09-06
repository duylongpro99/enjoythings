package saga

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"
)

func TestListFraudReviewsReturnsHeldSagasLongestHeldFirst(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	orchestrator, req := sagaUnderReview(ctx, t, store, &fakeWallet{}, &fakeLedger{}, &fakeOutbox{})

	older := heldSaga("55555555-5555-5555-5555-555555555555", fixedTime.Add(-time.Hour))
	if _, err := store.Create(ctx, older); err != nil {
		t.Fatalf("Create older held saga: %v", err)
	}
	processing := heldSaga("66666666-6666-6666-6666-666666666666", fixedTime.Add(-2*time.Hour))
	processing.State = StatePaymentProcessing
	if _, err := store.Create(ctx, processing); err != nil {
		t.Fatalf("Create processing saga: %v", err)
	}

	queue, err := orchestrator.ListFraudReviews(ctx)
	if err != nil {
		t.Fatalf("ListFraudReviews: %v", err)
	}
	want := []string{older.PaymentID, req.PaymentID}
	if got := paymentIDs(queue); !slices.Equal(got, want) {
		t.Fatalf("queue = %v, want %v", got, want)
	}

	if _, err := orchestrator.ResumeFraudReview(ctx, FraudReviewDecision{PaymentID: req.PaymentID, ActorID: "operator-1"}); err != nil {
		t.Fatalf("ResumeFraudReview: %v", err)
	}
	queue, err = orchestrator.ListFraudReviews(ctx)
	if err != nil {
		t.Fatalf("ListFraudReviews after resume: %v", err)
	}
	if got := paymentIDs(queue); !slices.Equal(got, []string{older.PaymentID}) {
		t.Fatalf("queue after resume = %v, want only %s", got, older.PaymentID)
	}
}

func TestGetFraudReviewReturnsSagaWithAuditTrailInOrder(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	orchestrator, req := sagaUnderReview(ctx, t, store, &fakeWallet{}, &fakeLedger{}, &fakeOutbox{})

	review, err := orchestrator.GetFraudReview(ctx, req.PaymentID)
	if err != nil {
		t.Fatalf("GetFraudReview: %v", err)
	}
	if review.Saga.State != StateFraudReview || review.Saga.FraudSessionID != "fraud-session-1" || review.Saga.FraudRiskScore != 0.95 {
		t.Fatalf("saga = %+v, want the held saga with its verdict", review.Saga)
	}
	if got := auditKinds(review.Audit); !slices.Equal(got, []string{FraudAuditKindTransition}) {
		t.Fatalf("audit kinds = %v, want only the transition", got)
	}

	if _, err := orchestrator.RejectFraudReview(ctx, FraudReviewDecision{PaymentID: req.PaymentID, ActorID: "operator-2", Reason: "confirmed fraud"}); err != nil {
		t.Fatalf("RejectFraudReview: %v", err)
	}

	review, err = orchestrator.GetFraudReview(ctx, req.PaymentID)
	if err != nil {
		t.Fatalf("GetFraudReview after reject: %v", err)
	}
	if review.Saga.State != StateFailed {
		t.Fatalf("state = %s, want %s: a decided review stays readable", review.Saga.State, StateFailed)
	}
	want := []string{FraudAuditKindTransition, FraudAuditKindReviewRejected}
	if got := auditKinds(review.Audit); !slices.Equal(got, want) {
		t.Fatalf("audit kinds = %v, want %v", got, want)
	}
	if last := review.Audit[1]; last.PaymentID != req.PaymentID || last.SagaState != StateFraudReview {
		t.Fatalf("decision audit = %+v, want the payment's record taken from %s", last, StateFraudReview)
	}
}

func TestGetFraudReviewUnknownPaymentIsNotFound(t *testing.T) {
	orchestrator := NewOrchestrator(newMemoryStore(), &fakeVerification{status: VerificationVerified}, &fakeWallet{}, &fakeLedger{}, fixedClock{})

	_, err := orchestrator.GetFraudReview(context.Background(), "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want %v", err, ErrNotFound)
	}
}

func TestGetFraudReviewWithoutAuditStoreHasEmptyTrail(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	orchestrator := NewOrchestrator(plainStore{store}, &fakeVerification{status: VerificationVerified}, &fakeWallet{}, &fakeLedger{}, fixedClock{})
	held := heldSaga("55555555-5555-5555-5555-555555555555", fixedTime)
	if _, err := store.Create(ctx, held); err != nil {
		t.Fatalf("Create held saga: %v", err)
	}

	review, err := orchestrator.GetFraudReview(ctx, held.PaymentID)
	if err != nil {
		t.Fatalf("GetFraudReview: %v", err)
	}
	if review.Saga.PaymentID != held.PaymentID || len(review.Audit) != 0 {
		t.Fatalf("review = %+v, want the saga with no trail", review)
	}
}

// plainStore hides the audit methods, so the orchestrator sees a Store that
// cannot persist a trail.
type plainStore struct{ Store }

func heldSaga(paymentID string, flaggedAt time.Time) Saga {
	return Saga{
		PaymentID:      paymentID,
		IdempotencyKey: paymentID,
		UserID:         "22222222-2222-2222-2222-222222222222",
		FromWalletID:   "33333333-3333-3333-3333-333333333333",
		ToWalletID:     "44444444-4444-4444-4444-444444444444",
		AmountCents:    500,
		Currency:       "USD",
		State:          StateFraudReview,
		FraudSessionID: "fraud-session-" + paymentID[:8],
		FraudAction:    "flag",
		FraudRiskScore: 0.8,
		FraudReason:    "unusual amount",
		FraudFlaggedAt: flaggedAt,
		CreatedAt:      flaggedAt,
		UpdatedAt:      flaggedAt,
	}
}

func paymentIDs(sagas []Saga) []string {
	ids := make([]string, 0, len(sagas))
	for _, current := range sagas {
		ids = append(ids, current.PaymentID)
	}
	return ids
}

func auditKinds(records []FraudAuditRecord) []string {
	kinds := make([]string, 0, len(records))
	for _, record := range records {
		kinds = append(kinds, record.Kind)
	}
	return kinds
}
