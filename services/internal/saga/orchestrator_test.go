package saga

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestStartPaymentSagaPersistsStartedBeforeCheckingVerification(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	req := startRequest()
	verification := &fakeVerification{status: VerificationVerified}
	verification.onGetStatus = func() {
		got, err := store.GetByPaymentID(ctx, req.PaymentID)
		if err != nil {
			t.Fatalf("saga not persisted before verification: %v", err)
		}
		if got.State != StateStarted {
			t.Fatalf("state before verification = %s, want %s", got.State, StateStarted)
		}
	}
	orchestrator := NewOrchestrator(store, verification, &fakeWallet{}, &fakeLedger{}, fixedClock{})

	got, err := orchestrator.StartPaymentSaga(ctx, req)
	if err != nil {
		t.Fatalf("StartPaymentSaga: %v", err)
	}
	if got.PaymentID != req.PaymentID || got.State != StatePaymentProcessing {
		t.Fatalf("saga = %+v, want payment %s processing", got, req.PaymentID)
	}
	if verification.calls != 1 {
		t.Fatalf("verification calls = %d, want 1", verification.calls)
	}
}

func TestStartPaymentSagaDuplicateReturnsExistingWithoutSideEffects(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	req := startRequest()
	orchestrator := NewOrchestrator(store, &fakeVerification{status: VerificationVerified}, &fakeWallet{}, &fakeLedger{}, fixedClock{})

	first, err := orchestrator.StartPaymentSaga(ctx, req)
	if err != nil {
		t.Fatalf("first StartPaymentSaga: %v", err)
	}
	second, err := orchestrator.StartPaymentSaga(ctx, req)
	if err != nil {
		t.Fatalf("duplicate StartPaymentSaga: %v", err)
	}
	if second.ID != first.ID || second.PaymentID != first.PaymentID || second.State != first.State {
		t.Fatalf("duplicate saga = %+v, want existing %+v", second, first)
	}
	if store.createCalls != 2 {
		t.Fatalf("create calls = %d, want 2 attempts", store.createCalls)
	}
}

func TestStartPaymentSagaDuplicateWithDifferentPayloadReturnsAlreadyExists(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	req := startRequest()
	orchestrator := NewOrchestrator(store, &fakeVerification{status: VerificationVerified}, &fakeWallet{}, &fakeLedger{}, fixedClock{})

	if _, err := orchestrator.StartPaymentSaga(ctx, req); err != nil {
		t.Fatalf("first StartPaymentSaga: %v", err)
	}
	req.AmountCents++
	_, err := orchestrator.StartPaymentSaga(ctx, req)
	if !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("conflicting duplicate error = %v, want %v", err, ErrAlreadyExists)
	}
}

func TestPaymentCompletedConfirmsLedgerAndPublishesTxCompleted(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	outbox := &fakeOutbox{}
	ledger := &fakeLedger{}
	orchestrator := NewOrchestrator(store, &fakeVerification{status: VerificationVerified}, &fakeWallet{}, ledger, fixedClock{})
	orchestrator.SetOutbox(outbox)
	req := startRequest()
	started, err := orchestrator.StartPaymentSaga(ctx, req)
	if err != nil {
		t.Fatalf("StartPaymentSaga: %v", err)
	}

	if err := orchestrator.HandlePaymentCompleted(ctx, PaymentCompleted{
		EventID:            "payment.completed:" + req.PaymentID,
		PaymentID:          req.PaymentID,
		TraceID:            req.TraceID,
		ProcessorPaymentID: "processor-1",
		Status:             "COMPLETED",
		CompletedAt:        fixedTime,
		OccurredAt:         fixedTime,
	}); err != nil {
		t.Fatalf("HandlePaymentCompleted: %v", err)
	}

	got, err := store.GetByPaymentID(ctx, req.PaymentID)
	if err != nil {
		t.Fatalf("GetByPaymentID: %v", err)
	}
	if got.State != StateCompleted {
		t.Fatalf("state = %s, want %s", got.State, StateCompleted)
	}
	if ledger.confirmedPaymentID != req.PaymentID || ledger.confirmedReservationID != started.LedgerReservationID {
		t.Fatalf("ledger confirm = %s/%s, want %s/%s", ledger.confirmedPaymentID, ledger.confirmedReservationID, req.PaymentID, started.LedgerReservationID)
	}
	if len(outbox.events) != 2 {
		t.Fatalf("outbox events = %d, want payment.execute and tx.completed", len(outbox.events))
	}
	if outbox.events[1].topic != TopicTxCompleted || outbox.events[1].partitionKey != req.FromWalletID {
		t.Fatalf("terminal event = %+v, want tx.completed partitioned by source wallet", outbox.events[1])
	}
	var payload TxCompleted
	if err := json.Unmarshal(outbox.events[1].payload, &payload); err != nil {
		t.Fatalf("unmarshal tx.completed: %v", err)
	}
	if payload.PaymentID != req.PaymentID || payload.TransferID != ledger.transferID {
		t.Fatalf("tx.completed payload = %+v", payload)
	}
}

func TestPaymentFailedCancelsLedgerCompensatesWalletAndPublishesTxFailed(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	outbox := &fakeOutbox{}
	wallet := &fakeWallet{}
	ledger := &fakeLedger{}
	orchestrator := NewOrchestrator(store, &fakeVerification{status: VerificationVerified}, wallet, ledger, fixedClock{})
	orchestrator.SetOutbox(outbox)
	req := startRequest()
	started, err := orchestrator.StartPaymentSaga(ctx, req)
	if err != nil {
		t.Fatalf("StartPaymentSaga: %v", err)
	}

	if err := orchestrator.HandlePaymentFailed(ctx, PaymentFailed{
		EventID:        "payment.failed:" + req.PaymentID,
		PaymentID:      req.PaymentID,
		TraceID:        req.TraceID,
		FailureCode:    "rail_declined",
		FailureMessage: "payment rail declined",
		FailedAt:       fixedTime,
		OccurredAt:     fixedTime,
	}); err != nil {
		t.Fatalf("HandlePaymentFailed: %v", err)
	}

	got, err := store.GetByPaymentID(ctx, req.PaymentID)
	if err != nil {
		t.Fatalf("GetByPaymentID: %v", err)
	}
	if got.State != StateFailed {
		t.Fatalf("state = %s, want %s", got.State, StateFailed)
	}
	if ledger.cancelledReservationID != started.LedgerReservationID {
		t.Fatalf("cancelled reservation = %s, want %s", ledger.cancelledReservationID, started.LedgerReservationID)
	}
	if wallet.compensatedDebitID != started.WalletDebitID {
		t.Fatalf("compensated debit = %s, want %s", wallet.compensatedDebitID, started.WalletDebitID)
	}
	if len(outbox.events) != 2 {
		t.Fatalf("outbox events = %d, want payment.execute and tx.failed", len(outbox.events))
	}
	if outbox.events[1].topic != TopicTxFailed {
		t.Fatalf("terminal topic = %s, want %s", outbox.events[1].topic, TopicTxFailed)
	}
	var payload TxFailed
	if err := json.Unmarshal(outbox.events[1].payload, &payload); err != nil {
		t.Fatalf("unmarshal tx.failed: %v", err)
	}
	if payload.PaymentID != req.PaymentID || payload.FailureCode != "rail_declined" {
		t.Fatalf("tx.failed payload = %+v", payload)
	}
}

func TestResumeNonTerminalSagasContinuesFromLastDurableState(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	req := startRequest()
	created := fixedTime.Add(-time.Minute)
	if _, err := store.Create(ctx, Saga{
		PaymentID:      req.PaymentID,
		IdempotencyKey: req.IdempotencyKey,
		UserID:         req.UserID,
		FromWalletID:   req.FromWalletID,
		ToWalletID:     req.ToWalletID,
		AmountCents:    req.AmountCents,
		Currency:       req.Currency,
		State:          StateWalletDebited,
		WalletDebitID:  "wallet-debit-existing",
		CreatedAt:      created,
		UpdatedAt:      created,
	}); err != nil {
		t.Fatalf("seed saga: %v", err)
	}
	ledger := &fakeLedger{}
	outbox := &fakeOutbox{}
	orchestrator := NewOrchestrator(store, &fakeVerification{status: VerificationVerified}, &fakeWallet{}, ledger, fixedClock{})
	orchestrator.SetOutbox(outbox)

	if err := orchestrator.ResumeNonTerminal(ctx); err != nil {
		t.Fatalf("ResumeNonTerminal: %v", err)
	}
	got, err := store.GetByPaymentID(ctx, req.PaymentID)
	if err != nil {
		t.Fatalf("GetByPaymentID: %v", err)
	}
	if got.State != StatePaymentProcessing {
		t.Fatalf("state = %s, want %s", got.State, StatePaymentProcessing)
	}
	if ledger.reserveCalls != 1 {
		t.Fatalf("ledger reserve calls = %d, want 1", ledger.reserveCalls)
	}
	if len(outbox.events) != 1 || outbox.events[0].topic != TopicPaymentExecute {
		t.Fatalf("outbox events = %+v, want one payment.execute", outbox.events)
	}
}

func TestResumeLedgerConfirmedPublishesCompletedAndMarksCompleted(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	req := startRequest()
	if _, err := store.Create(ctx, Saga{
		PaymentID:           req.PaymentID,
		IdempotencyKey:      req.IdempotencyKey,
		UserID:              req.UserID,
		FromWalletID:        req.FromWalletID,
		ToWalletID:          req.ToWalletID,
		AmountCents:         req.AmountCents,
		Currency:            req.Currency,
		State:               StateLedgerConfirmed,
		WalletDebitID:       "wallet-debit-existing",
		LedgerReservationID: "ledger-reservation-existing",
		TransferID:          "transfer-existing",
		CreatedAt:           fixedTime,
		UpdatedAt:           fixedTime,
	}); err != nil {
		t.Fatalf("seed saga: %v", err)
	}
	outbox := &fakeOutbox{}
	orchestrator := NewOrchestrator(store, &fakeVerification{status: VerificationVerified}, &fakeWallet{}, &fakeLedger{}, fixedClock{})
	orchestrator.SetOutbox(outbox)

	if err := orchestrator.ResumeNonTerminal(ctx); err != nil {
		t.Fatalf("ResumeNonTerminal: %v", err)
	}
	got, err := store.GetByPaymentID(ctx, req.PaymentID)
	if err != nil {
		t.Fatalf("GetByPaymentID: %v", err)
	}
	if got.State != StateCompleted {
		t.Fatalf("state = %s, want %s", got.State, StateCompleted)
	}
	if len(outbox.events) != 1 || outbox.events[0].topic != TopicTxCompleted {
		t.Fatalf("outbox events = %+v, want tx.completed", outbox.events)
	}
}

func TestResumeCompensatingWalletCompensatesAndPublishesFailed(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	req := startRequest()
	if _, err := store.Create(ctx, Saga{
		PaymentID:           req.PaymentID,
		IdempotencyKey:      req.IdempotencyKey,
		UserID:              req.UserID,
		FromWalletID:        req.FromWalletID,
		ToWalletID:          req.ToWalletID,
		AmountCents:         req.AmountCents,
		Currency:            req.Currency,
		State:               StateCompensatingWallet,
		WalletDebitID:       "wallet-debit-existing",
		LedgerReservationID: "ledger-reservation-existing",
		FailureCode:         "rail_declined",
		LastError:           "payment rail declined",
		CreatedAt:           fixedTime,
		UpdatedAt:           fixedTime,
	}); err != nil {
		t.Fatalf("seed saga: %v", err)
	}
	wallet := &fakeWallet{}
	outbox := &fakeOutbox{}
	orchestrator := NewOrchestrator(store, &fakeVerification{status: VerificationVerified}, wallet, &fakeLedger{}, fixedClock{})
	orchestrator.SetOutbox(outbox)

	if err := orchestrator.ResumeNonTerminal(ctx); err != nil {
		t.Fatalf("ResumeNonTerminal: %v", err)
	}
	got, err := store.GetByPaymentID(ctx, req.PaymentID)
	if err != nil {
		t.Fatalf("GetByPaymentID: %v", err)
	}
	if got.State != StateFailed {
		t.Fatalf("state = %s, want %s", got.State, StateFailed)
	}
	if wallet.compensatedDebitID != "wallet-debit-existing" {
		t.Fatalf("compensated debit = %s, want wallet-debit-existing", wallet.compensatedDebitID)
	}
	if len(outbox.events) != 1 || outbox.events[0].topic != TopicTxFailed {
		t.Fatalf("outbox events = %+v, want tx.failed", outbox.events)
	}
}

func startRequest() StartRequest {
	return StartRequest{
		PaymentID:      "11111111-1111-1111-1111-111111111111",
		IdempotencyKey: "idem-1",
		TraceID:        "trace-1",
		UserID:         "22222222-2222-2222-2222-222222222222",
		FromWalletID:   "33333333-3333-3333-3333-333333333333",
		ToWalletID:     "44444444-4444-4444-4444-444444444444",
		AmountCents:    1250,
		Currency:       "USD",
	}
}

var fixedTime = time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)

type fixedClock struct{}

func (fixedClock) Now() time.Time {
	return fixedTime
}

type fakeVerification struct {
	status      string
	calls       int
	onGetStatus func()
}

func (verification *fakeVerification) GetStatus(context.Context, VerificationRequest) (VerificationResult, error) {
	verification.calls++
	if verification.onGetStatus != nil {
		verification.onGetStatus()
	}
	return VerificationResult{Status: verification.status}, nil
}

type fakeWallet struct {
	compensatedDebitID string
}

func (wallet *fakeWallet) DebitForSaga(context.Context, WalletDebitRequest) (WalletDebitResult, error) {
	return WalletDebitResult{WalletDebitID: "wallet-debit-1"}, nil
}

func (wallet *fakeWallet) CompensateDebit(_ context.Context, req WalletCompensateRequest) error {
	wallet.compensatedDebitID = req.WalletDebitID
	return nil
}

type fakeLedger struct {
	reserveCalls           int
	transferID             string
	confirmedPaymentID     string
	confirmedReservationID string
	cancelledReservationID string
}

func (ledger *fakeLedger) ReserveTransfer(context.Context, LedgerReserveRequest) (LedgerReserveResult, error) {
	ledger.reserveCalls++
	return LedgerReserveResult{LedgerReservationID: "ledger-reservation-1"}, nil
}

func (ledger *fakeLedger) ConfirmTransfer(_ context.Context, req LedgerConfirmRequest) (LedgerConfirmResult, error) {
	ledger.transferID = "transfer-1"
	ledger.confirmedPaymentID = req.PaymentID
	ledger.confirmedReservationID = req.LedgerReservationID
	return LedgerConfirmResult{TransferID: ledger.transferID, CompletedAt: fixedTime}, nil
}

func (ledger *fakeLedger) CancelReservation(_ context.Context, req LedgerCancelRequest) error {
	ledger.cancelledReservationID = req.LedgerReservationID
	return nil
}

type fakeOutbox struct {
	events []fakeOutboxEvent
}

func (outbox *fakeOutbox) Enqueue(_ context.Context, topic, partitionKey string, payload []byte) error {
	outbox.events = append(outbox.events, fakeOutboxEvent{
		topic:        topic,
		partitionKey: partitionKey,
		payload:      append([]byte(nil), payload...),
	})
	return nil
}

type fakeOutboxEvent struct {
	topic        string
	partitionKey string
	payload      []byte
}
