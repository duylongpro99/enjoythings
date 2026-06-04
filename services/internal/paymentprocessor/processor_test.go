package paymentprocessor

import (
	"context"
	"errors"
	"testing"
	"time"

	"enjoythings/services/internal/saga"
)

func TestProcessorPublishesCompletedAndSkipsRailForDuplicateCompletedPayment(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	rail := &fakeRail{}
	outbox := &fakeOutbox{}
	processor := NewProcessor(store, rail, outbox, ProcessorConfig{Backoffs: []time.Duration{time.Millisecond}, Jitter: noJitter})
	command := paymentExecuteFixture()

	if err := processor.HandleExecute(ctx, command); err != nil {
		t.Fatalf("first HandleExecute: %v", err)
	}
	if rail.calls != 1 {
		t.Fatalf("rail calls after first command = %d, want 1", rail.calls)
	}

	if err := processor.HandleExecute(ctx, command); err != nil {
		t.Fatalf("duplicate HandleExecute: %v", err)
	}
	if rail.calls != 1 {
		t.Fatalf("rail calls after duplicate command = %d, want still 1", rail.calls)
	}
	if len(outbox.events) != 2 {
		t.Fatalf("published events = %d, want completion republished twice", len(outbox.events))
	}
	if outbox.events[0].topic != saga.TopicPaymentCompleted || outbox.events[1].topic != saga.TopicPaymentCompleted {
		t.Fatalf("published topics = %+v, want payment.completed twice", outbox.events)
	}
}

func TestProcessorRetriesRetryableRailFailuresThenPublishesCompleted(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	rail := &fakeRail{
		results: []railCallResult{
			{err: RetryableRailError("rail_unavailable", "rail unavailable")},
			{result: RailResult{ProcessorPaymentID: "processor-payment-2", CompletedAt: time.Date(2026, 6, 4, 1, 2, 3, 0, time.UTC)}},
		},
	}
	outbox := &fakeOutbox{}
	sleeper := &recordingSleeper{}
	processor := NewProcessor(store, rail, outbox, ProcessorConfig{
		Backoffs: []time.Duration{time.Second, 3 * time.Second, 9 * time.Second},
		Jitter:   noJitter,
		Sleeper:  sleeper,
	})

	if err := processor.HandleExecute(ctx, paymentExecuteFixture()); err != nil {
		t.Fatalf("HandleExecute: %v", err)
	}
	if rail.calls != 2 {
		t.Fatalf("rail calls = %d, want 2", rail.calls)
	}
	if len(sleeper.durations) != 1 || sleeper.durations[0] != time.Second {
		t.Fatalf("sleep durations = %v, want [1s]", sleeper.durations)
	}
	if len(outbox.events) != 1 || outbox.events[0].topic != saga.TopicPaymentCompleted {
		t.Fatalf("published events = %+v, want one payment.completed", outbox.events)
	}
}

func TestProcessorFailsAfterRetryableRailFailuresReachConfiguredLimit(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	rail := &fakeRail{
		results: []railCallResult{
			{err: RetryableRailError("rail_unavailable", "rail unavailable")},
			{err: RetryableRailError("rail_unavailable", "rail unavailable")},
			{err: RetryableRailError("rail_unavailable", "rail unavailable")},
		},
	}
	outbox := &fakeOutbox{}
	sleeper := &recordingSleeper{}
	processor := NewProcessor(store, rail, outbox, ProcessorConfig{
		Backoffs: []time.Duration{time.Second, 3 * time.Second},
		Jitter:   noJitter,
		Sleeper:  sleeper,
	})

	if err := processor.HandleExecute(ctx, paymentExecuteFixture()); err != nil {
		t.Fatalf("HandleExecute: %v", err)
	}
	if rail.calls != 3 {
		t.Fatalf("rail calls = %d, want 3", rail.calls)
	}
	if len(sleeper.durations) != 2 {
		t.Fatalf("sleep count = %d, want 2", len(sleeper.durations))
	}
	if len(outbox.events) != 1 || outbox.events[0].topic != saga.TopicPaymentFailed {
		t.Fatalf("published events = %+v, want one payment.failed", outbox.events)
	}
	got, err := store.GetByPaymentID(ctx, "payment-1")
	if err != nil {
		t.Fatalf("GetByPaymentID: %v", err)
	}
	if got.Status != StatusFailed || got.AttemptCount != 3 {
		t.Fatalf("attempt = %+v, want failed after 3 attempts", got)
	}
}

func TestProcessorRetriesPendingAttemptAlreadyAtLimitAfterRestart(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	command := paymentExecuteFixture()
	store.attempts[command.PaymentID] = Attempt{
		PaymentID:      command.PaymentID,
		IdempotencyKey: command.IdempotencyKey,
		TraceID:        command.TraceID,
		AmountCents:    command.AmountCents,
		Currency:       command.Currency,
		Status:         StatusPending,
		AttemptCount:   3,
	}
	rail := &fakeRail{}
	outbox := &fakeOutbox{}
	processor := NewProcessor(store, rail, outbox, ProcessorConfig{
		Backoffs: []time.Duration{time.Second, 3 * time.Second},
		Jitter:   noJitter,
		Sleeper:  &recordingSleeper{},
	})

	if err := processor.HandleExecute(ctx, command); err != nil {
		t.Fatalf("HandleExecute: %v", err)
	}
	if rail.calls != 1 {
		t.Fatalf("rail calls = %d, want recovery retry", rail.calls)
	}
	if len(outbox.events) != 1 || outbox.events[0].topic != saga.TopicPaymentCompleted {
		t.Fatalf("published events = %+v, want one payment.completed", outbox.events)
	}
}

func TestProcessorPublishesFailedForTerminalRailFailure(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	rail := &fakeRail{results: []railCallResult{{err: TerminalRailError("card_declined", "card declined")}}}
	outbox := &fakeOutbox{}
	processor := NewProcessor(store, rail, outbox, ProcessorConfig{Jitter: noJitter})

	if err := processor.HandleExecute(ctx, paymentExecuteFixture()); err != nil {
		t.Fatalf("HandleExecute: %v", err)
	}
	if rail.calls != 1 {
		t.Fatalf("rail calls = %d, want 1", rail.calls)
	}
	if len(outbox.events) != 1 || outbox.events[0].topic != saga.TopicPaymentFailed {
		t.Fatalf("published events = %+v, want one payment.failed", outbox.events)
	}
	got, err := store.GetByPaymentID(ctx, "payment-1")
	if err != nil {
		t.Fatalf("GetByPaymentID: %v", err)
	}
	if got.Status != StatusFailed || got.FailureCode != "card_declined" {
		t.Fatalf("attempt = %+v, want failed card_declined", got)
	}
}

func TestProcessorReturnsErrorAndDoesNotPublishWhenOutboxFails(t *testing.T) {
	ctx := context.Background()
	store := newMemoryStore()
	rail := &fakeRail{}
	outbox := &fakeOutbox{err: errors.New("outbox unavailable")}
	processor := NewProcessor(store, rail, outbox, ProcessorConfig{Jitter: noJitter})

	err := processor.HandleExecute(ctx, paymentExecuteFixture())
	if err == nil {
		t.Fatal("expected outbox error")
	}
	if outbox.published != 0 {
		t.Fatalf("published events = %d, want 0 successful publishes", outbox.published)
	}
}

func paymentExecuteFixture() saga.PaymentExecute {
	return saga.PaymentExecute{
		EventID:             "payment.execute:payment-1",
		PaymentID:           "payment-1",
		IdempotencyKey:      "payment-1:execute-payment",
		TraceID:             "trace-1",
		FromWalletID:        "from-wallet-1",
		ToWalletID:          "to-wallet-1",
		AmountCents:         1250,
		Currency:            "USD",
		LedgerReservationID: "ledger-reservation-1",
		WalletDebitID:       "wallet-debit-1",
		OccurredAt:          time.Date(2026, 6, 4, 0, 0, 0, 0, time.UTC),
	}
}

func noJitter(duration time.Duration) time.Duration {
	return duration
}

type railCallResult struct {
	result RailResult
	err    error
}

type fakeRail struct {
	calls   int
	results []railCallResult
}

func (rail *fakeRail) Charge(context.Context, RailChargeRequest) (RailResult, error) {
	rail.calls++
	if len(rail.results) == 0 {
		return RailResult{ProcessorPaymentID: "processor-payment-1", CompletedAt: time.Date(2026, 6, 4, 1, 0, 0, 0, time.UTC)}, nil
	}
	next := rail.results[0]
	rail.results = rail.results[1:]
	return next.result, next.err
}

type fakeOutbox struct {
	events    []publishedEvent
	err       error
	published int
}

type publishedEvent struct {
	topic        string
	partitionKey string
	payload      []byte
}

func (outbox *fakeOutbox) Enqueue(_ context.Context, topic, partitionKey string, payload []byte) error {
	if outbox.err != nil {
		return outbox.err
	}
	outbox.published++
	outbox.events = append(outbox.events, publishedEvent{topic: topic, partitionKey: partitionKey, payload: append([]byte(nil), payload...)})
	return nil
}

type recordingSleeper struct {
	durations []time.Duration
}

func (sleeper *recordingSleeper) Sleep(_ context.Context, duration time.Duration) error {
	sleeper.durations = append(sleeper.durations, duration)
	return nil
}

type memoryStore struct {
	attempts map[string]Attempt
}

func newMemoryStore() *memoryStore {
	return &memoryStore{attempts: make(map[string]Attempt)}
}

func (store *memoryStore) GetByPaymentID(_ context.Context, paymentID string) (Attempt, error) {
	attempt, ok := store.attempts[paymentID]
	if !ok {
		return Attempt{}, ErrNotFound
	}
	return attempt, nil
}

func (store *memoryStore) CreatePending(_ context.Context, command saga.PaymentExecute, now time.Time) (Attempt, error) {
	if existing, ok := store.attempts[command.PaymentID]; ok {
		return existing, nil
	}
	attempt := Attempt{
		PaymentID:           command.PaymentID,
		IdempotencyKey:      command.IdempotencyKey,
		TraceID:             command.TraceID,
		AmountCents:         command.AmountCents,
		Currency:            command.Currency,
		LedgerReservationID: command.LedgerReservationID,
		WalletDebitID:       command.WalletDebitID,
		Status:              StatusPending,
		CreatedAt:           now,
		UpdatedAt:           now,
	}
	store.attempts[command.PaymentID] = attempt
	return attempt, nil
}

func (store *memoryStore) MarkAttemptStarted(_ context.Context, paymentID string, attemptCount int, now time.Time) (Attempt, error) {
	attempt, ok := store.attempts[paymentID]
	if !ok {
		return Attempt{}, ErrNotFound
	}
	attempt.AttemptCount = attemptCount
	attempt.UpdatedAt = now
	store.attempts[paymentID] = attempt
	return attempt, nil
}

func (store *memoryStore) MarkCompleted(_ context.Context, paymentID string, result RailResult, now time.Time) (Attempt, error) {
	attempt, ok := store.attempts[paymentID]
	if !ok {
		return Attempt{}, ErrNotFound
	}
	attempt.Status = StatusCompleted
	attempt.ProcessorPaymentID = result.ProcessorPaymentID
	attempt.CompletedAt = result.CompletedAt
	attempt.UpdatedAt = now
	store.attempts[paymentID] = attempt
	return attempt, nil
}

func (store *memoryStore) MarkFailed(_ context.Context, paymentID string, failure RailFailure, now time.Time) (Attempt, error) {
	attempt, ok := store.attempts[paymentID]
	if !ok {
		return Attempt{}, ErrNotFound
	}
	attempt.Status = StatusFailed
	attempt.FailureCode = failure.Code
	attempt.FailureMessage = failure.Message
	attempt.FailedAt = now
	attempt.UpdatedAt = now
	store.attempts[paymentID] = attempt
	return attempt, nil
}
