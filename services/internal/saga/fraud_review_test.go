package saga

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"enjoythings/services/internal/event"
)

func TestResumeFraudReviewAppliesDeferredCompletion(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	ledger := &fakeLedger{}
	outbox := &fakeOutbox{}
	orchestrator, req := sagaUnderReview(ctx, t, store, &fakeWallet{}, ledger, outbox)

	completed := PaymentCompleted{
		EventID:            TopicPaymentCompleted + ":" + req.PaymentID,
		PaymentID:          req.PaymentID,
		IdempotencyKey:     req.IdempotencyKey,
		TraceID:            req.TraceID,
		ProcessorPaymentID: "rail-payment-1",
		Status:             "succeeded",
		CompletedAt:        fixedTime,
		OccurredAt:         fixedTime,
	}
	if err := orchestrator.HandlePaymentCompleted(ctx, completed); err != nil {
		t.Fatalf("HandlePaymentCompleted during review: %v", err)
	}
	held, _ := store.GetByPaymentID(ctx, req.PaymentID)
	if held.State != StateFraudReview || held.DeferredPaymentJSON == "" {
		t.Fatalf("saga = %s deferred=%q, want %s with a deferred result", held.State, held.DeferredPaymentJSON, StateFraudReview)
	}

	resumed, err := orchestrator.ResumeFraudReview(ctx, FraudReviewDecision{
		PaymentID: req.PaymentID,
		ActorID:   "operator-1",
		Reason:    "manual check cleared",
		TraceID:   "trace-review",
	})
	if err != nil {
		t.Fatalf("ResumeFraudReview: %v", err)
	}
	if resumed.State != StateCompleted {
		t.Fatalf("state = %s, want %s", resumed.State, StateCompleted)
	}
	if resumed.DeferredPaymentJSON != "" {
		t.Fatalf("deferred result = %q, want cleared", resumed.DeferredPaymentJSON)
	}
	if ledger.confirmedReservationID != "ledger-reservation-1" {
		t.Fatalf("confirmed reservation = %q, want the reserved one", ledger.confirmedReservationID)
	}
	if !outboxHasTopic(outbox, TopicTxCompleted) {
		t.Fatalf("outbox topics = %v, want %s", outboxTopics(outbox), TopicTxCompleted)
	}
	assertReviewAudit(t, store, FraudAuditKindReviewResumed, "operator-1", "manual check cleared")
}

func TestResumeFraudReviewAppliesDeferredFailureThroughCompensation(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	wallet := &fakeWallet{}
	ledger := &fakeLedger{}
	outbox := &fakeOutbox{}
	orchestrator, req := sagaUnderReview(ctx, t, store, wallet, ledger, outbox)

	failed := PaymentFailed{
		EventID:        TopicPaymentFailed + ":" + req.PaymentID,
		PaymentID:      req.PaymentID,
		IdempotencyKey: req.IdempotencyKey,
		TraceID:        req.TraceID,
		FailureCode:    "rail_declined",
		FailureMessage: "declined by rail",
		FailedAt:       fixedTime,
		OccurredAt:     fixedTime,
	}
	if err := orchestrator.HandlePaymentFailed(ctx, failed); err != nil {
		t.Fatalf("HandlePaymentFailed during review: %v", err)
	}

	resumed, err := orchestrator.ResumeFraudReview(ctx, FraudReviewDecision{PaymentID: req.PaymentID, ActorID: "operator-1"})
	if err != nil {
		t.Fatalf("ResumeFraudReview: %v", err)
	}
	if resumed.State != StateFailed {
		t.Fatalf("state = %s, want %s", resumed.State, StateFailed)
	}
	if resumed.FailureCode != "rail_declined" {
		t.Fatalf("failure code = %q, want the rail code", resumed.FailureCode)
	}
	if wallet.compensatedDebitID != "wallet-debit-1" || ledger.cancelledReservationID != "ledger-reservation-1" {
		t.Fatalf("compensation = wallet %q ledger %q, want both compensated", wallet.compensatedDebitID, ledger.cancelledReservationID)
	}
	if !outboxHasTopic(outbox, TopicTxFailed) {
		t.Fatalf("outbox topics = %v, want %s", outboxTopics(outbox), TopicTxFailed)
	}
}

func TestResumeFraudReviewWithoutDeferredResultWaitsForPayment(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	ledger := &fakeLedger{}
	outbox := &fakeOutbox{}
	orchestrator, req := sagaUnderReview(ctx, t, store, &fakeWallet{}, ledger, outbox)

	resumed, err := orchestrator.ResumeFraudReview(ctx, FraudReviewDecision{PaymentID: req.PaymentID, ActorID: "operator-1"})
	if err != nil {
		t.Fatalf("ResumeFraudReview: %v", err)
	}
	if resumed.State != StatePaymentProcessing {
		t.Fatalf("state = %s, want %s", resumed.State, StatePaymentProcessing)
	}
	if outboxHasTopic(outbox, TopicTxCompleted) || outboxHasTopic(outbox, TopicTxFailed) {
		t.Fatalf("outbox topics = %v, want no terminal event yet", outboxTopics(outbox))
	}

	if err := orchestrator.HandlePaymentCompleted(ctx, PaymentCompleted{
		EventID:     TopicPaymentCompleted + ":" + req.PaymentID,
		PaymentID:   req.PaymentID,
		TraceID:     req.TraceID,
		Status:      "succeeded",
		CompletedAt: fixedTime,
		OccurredAt:  fixedTime,
	}); err != nil {
		t.Fatalf("HandlePaymentCompleted after resume: %v", err)
	}
	final, _ := store.GetByPaymentID(ctx, req.PaymentID)
	if final.State != StateCompleted {
		t.Fatalf("state after payment result = %s, want %s", final.State, StateCompleted)
	}
}

func TestRejectFraudReviewCompensatesAndFailsSaga(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	wallet := &fakeWallet{}
	ledger := &fakeLedger{}
	outbox := &fakeOutbox{}
	orchestrator, req := sagaUnderReview(ctx, t, store, wallet, ledger, outbox)

	rejected, err := orchestrator.RejectFraudReview(ctx, FraudReviewDecision{
		PaymentID: req.PaymentID,
		ActorID:   "operator-2",
		Reason:    "confirmed fraud",
		TraceID:   "trace-review",
	})
	if err != nil {
		t.Fatalf("RejectFraudReview: %v", err)
	}
	if rejected.State != StateFailed {
		t.Fatalf("state = %s, want %s", rejected.State, StateFailed)
	}
	if rejected.FailureCode != FailureCodeFraudRejected || rejected.LastError != "confirmed fraud" {
		t.Fatalf("failure = %q/%q, want %q with the operator reason", rejected.FailureCode, rejected.LastError, FailureCodeFraudRejected)
	}
	if wallet.compensatedDebitID != "wallet-debit-1" || ledger.cancelledReservationID != "ledger-reservation-1" {
		t.Fatalf("compensation = wallet %q ledger %q, want both compensated", wallet.compensatedDebitID, ledger.cancelledReservationID)
	}
	if !outboxHasTopic(outbox, TopicTxFailed) {
		t.Fatalf("outbox topics = %v, want %s", outboxTopics(outbox), TopicTxFailed)
	}
	assertReviewAudit(t, store, FraudAuditKindReviewRejected, "operator-2", "confirmed fraud")
}

func TestRejectFraudReviewKeepsDeferredResultForAudit(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	orchestrator, req := sagaUnderReview(ctx, t, store, &fakeWallet{}, &fakeLedger{}, &fakeOutbox{})

	if err := orchestrator.HandlePaymentCompleted(ctx, PaymentCompleted{
		EventID:     TopicPaymentCompleted + ":" + req.PaymentID,
		PaymentID:   req.PaymentID,
		TraceID:     req.TraceID,
		Status:      "succeeded",
		CompletedAt: fixedTime,
		OccurredAt:  fixedTime,
	}); err != nil {
		t.Fatalf("HandlePaymentCompleted during review: %v", err)
	}

	rejected, err := orchestrator.RejectFraudReview(ctx, FraudReviewDecision{PaymentID: req.PaymentID, ActorID: "operator-2"})
	if err != nil {
		t.Fatalf("RejectFraudReview: %v", err)
	}
	if rejected.DeferredPaymentJSON == "" {
		t.Fatal("deferred result was cleared, want it kept for the audit trail")
	}
	if rejected.LastError != defaultRejectReason {
		t.Fatalf("reason = %q, want the default %q", rejected.LastError, defaultRejectReason)
	}
}

func TestFraudReviewDecisionsRequireReviewState(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	orchestrator := NewOrchestrator(store, &fakeVerification{status: VerificationVerified}, &fakeWallet{}, &fakeLedger{}, fixedClock{})
	orchestrator.SetOutbox(&fakeOutbox{})
	req := startRequest()
	if _, err := orchestrator.StartPaymentSaga(ctx, req); err != nil {
		t.Fatalf("StartPaymentSaga: %v", err)
	}

	if _, err := orchestrator.ResumeFraudReview(ctx, FraudReviewDecision{PaymentID: req.PaymentID}); !errors.Is(err, ErrNotUnderReview) {
		t.Fatalf("resume error = %v, want %v", err, ErrNotUnderReview)
	}
	if _, err := orchestrator.RejectFraudReview(ctx, FraudReviewDecision{PaymentID: req.PaymentID}); !errors.Is(err, ErrNotUnderReview) {
		t.Fatalf("reject error = %v, want %v", err, ErrNotUnderReview)
	}
	current, _ := store.GetByPaymentID(ctx, req.PaymentID)
	if current.State != StatePaymentProcessing {
		t.Fatalf("state = %s, want it untouched at %s", current.State, StatePaymentProcessing)
	}

	if _, err := orchestrator.ResumeFraudReview(ctx, FraudReviewDecision{PaymentID: "55555555-5555-5555-5555-555555555555"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("resume of unknown payment = %v, want %v", err, ErrNotFound)
	}
}

func TestResumeFraudReviewIsRejectedAfterTheReviewEnds(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	orchestrator, req := sagaUnderReview(ctx, t, store, &fakeWallet{}, &fakeLedger{}, &fakeOutbox{})

	if _, err := orchestrator.ResumeFraudReview(ctx, FraudReviewDecision{PaymentID: req.PaymentID, ActorID: "operator-1"}); err != nil {
		t.Fatalf("first ResumeFraudReview: %v", err)
	}
	if _, err := orchestrator.ResumeFraudReview(ctx, FraudReviewDecision{PaymentID: req.PaymentID, ActorID: "operator-1"}); !errors.Is(err, ErrNotUnderReview) {
		t.Fatalf("second resume = %v, want %v", err, ErrNotUnderReview)
	}
}

func TestResumeFraudReviewRejectsUnrecognizedDeferredResult(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	orchestrator, req := sagaUnderReview(ctx, t, store, &fakeWallet{}, &fakeLedger{}, &fakeOutbox{})

	held, _ := store.GetByPaymentID(ctx, req.PaymentID)
	held.DeferredPaymentJSON = `{"event_id":"payment.unknown:1"}`
	if _, err := store.Update(ctx, held); err != nil {
		t.Fatalf("seed deferred result: %v", err)
	}

	if _, err := orchestrator.ResumeFraudReview(ctx, FraudReviewDecision{PaymentID: req.PaymentID}); !errors.Is(err, ErrUnknownDeferredResult) {
		t.Fatalf("resume error = %v, want %v", err, ErrUnknownDeferredResult)
	}
}

func sagaUnderReview(ctx context.Context, t *testing.T, store *memoryStore, wallet *fakeWallet, ledger *fakeLedger, outbox *fakeOutbox) (*Orchestrator, StartRequest) {
	t.Helper()
	orchestrator := NewOrchestrator(store, &fakeVerification{status: VerificationVerified}, wallet, ledger, fixedClock{})
	orchestrator.SetOutbox(outbox)
	req := startRequest()
	if _, err := orchestrator.StartPaymentSaga(ctx, req); err != nil {
		t.Fatalf("StartPaymentSaga: %v", err)
	}
	sourceEventID := event.FraudScoreRequestedEventID(req.PaymentID)
	if err := orchestrator.HandleFraudFlagged(ctx, event.FraudFlagged{
		SchemaVersion: 1,
		EventID:       event.FraudFlaggedEventID(sourceEventID),
		SourceEventID: sourceEventID,
		PaymentID:     req.PaymentID,
		SessionID:     "fraud-session-1",
		Action:        event.FraudActionBlock,
		RiskScore:     0.95,
		Reason:        "high velocity",
		ProviderID:    "provider-1",
		ModelID:       "model-1",
		OccurredAt:    fixedTime,
		TraceID:       req.TraceID,
	}); err != nil {
		t.Fatalf("HandleFraudFlagged: %v", err)
	}
	current, _ := store.GetByPaymentID(ctx, req.PaymentID)
	if current.State != StateFraudReview {
		t.Fatalf("setup state = %s, want %s", current.State, StateFraudReview)
	}
	return orchestrator, req
}

func assertReviewAudit(t *testing.T, store *memoryStore, kind, actorID, reason string) {
	t.Helper()
	for _, audit := range store.audits {
		if audit.Kind != kind {
			continue
		}
		if audit.SagaState != StateFraudReview {
			t.Fatalf("audit saga state = %s, want %s", audit.SagaState, StateFraudReview)
		}
		var details struct {
			ActorID string `json:"actor_id"`
			Reason  string `json:"reason"`
		}
		if err := json.Unmarshal([]byte(audit.DetailsJSON), &details); err != nil {
			t.Fatalf("audit details %q: %v", audit.DetailsJSON, err)
		}
		if details.ActorID != actorID || details.Reason != reason {
			t.Fatalf("audit actor/reason = %q/%q, want %q/%q", details.ActorID, details.Reason, actorID, reason)
		}
		return
	}
	t.Fatalf("no %s audit record was written", kind)
}

func outboxTopics(outbox *fakeOutbox) []string {
	topics := make([]string, 0, len(outbox.events))
	for _, item := range outbox.events {
		topics = append(topics, item.topic)
	}
	return topics
}

func outboxHasTopic(outbox *fakeOutbox, topic string) bool {
	for _, item := range outbox.events {
		if item.topic == topic {
			return true
		}
	}
	return false
}
