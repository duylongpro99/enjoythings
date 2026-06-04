package paymentprocessor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"time"

	"enjoythings/services/internal/saga"
)

type Store interface {
	GetByPaymentID(context.Context, string) (Attempt, error)
	CreatePending(context.Context, saga.PaymentExecute, time.Time) (Attempt, error)
	MarkAttemptStarted(context.Context, string, int, time.Time) (Attempt, error)
	MarkCompleted(context.Context, string, RailResult, time.Time) (Attempt, error)
	MarkFailed(context.Context, string, RailFailure, time.Time) (Attempt, error)
}

type Rail interface {
	Charge(context.Context, RailChargeRequest) (RailResult, error)
}

type Outbox interface {
	Enqueue(context.Context, string, string, []byte) error
}

type Sleeper interface {
	Sleep(context.Context, time.Duration) error
}

type JitterFunc func(time.Duration) time.Duration

type Clock interface {
	Now() time.Time
}

type ProcessorConfig struct {
	Backoffs []time.Duration
	Jitter   JitterFunc
	Sleeper  Sleeper
	Clock    Clock
}

type Processor struct {
	store    Store
	rail     Rail
	outbox   Outbox
	backoffs []time.Duration
	jitter   JitterFunc
	sleeper  Sleeper
	clock    Clock
}

func NewProcessor(store Store, rail Rail, outbox Outbox, cfg ProcessorConfig) *Processor {
	backoffs := cfg.Backoffs
	if backoffs == nil {
		backoffs = []time.Duration{time.Second, 3 * time.Second, 9 * time.Second}
	}
	jitter := cfg.Jitter
	if jitter == nil {
		jitter = randomJitter
	}
	sleeper := cfg.Sleeper
	if sleeper == nil {
		sleeper = contextSleeper{}
	}
	clock := cfg.Clock
	if clock == nil {
		clock = systemClock{}
	}
	return &Processor{store: store, rail: rail, outbox: outbox, backoffs: backoffs, jitter: jitter, sleeper: sleeper, clock: clock}
}

func (processor *Processor) HandleExecute(ctx context.Context, command saga.PaymentExecute) error {
	if err := validateCommand(command); err != nil {
		return err
	}
	attempt, err := processor.store.CreatePending(ctx, command, processor.clock.Now())
	if err != nil {
		return err
	}
	switch attempt.Status {
	case StatusCompleted:
		return processor.publishCompleted(ctx, attempt)
	case StatusFailed:
		return processor.publishFailed(ctx, attempt)
	case StatusPending:
		return processor.processPending(ctx, attempt)
	default:
		return fmt.Errorf("unknown payment attempt status %q", attempt.Status)
	}
}

func (processor *Processor) processPending(ctx context.Context, attempt Attempt) error {
	maxAttempts := len(processor.backoffs) + 1
	firstAttempt := attempt.AttemptCount + 1
	if firstAttempt > maxAttempts {
		firstAttempt = maxAttempts
	}
	for nextAttempt := firstAttempt; nextAttempt <= maxAttempts; nextAttempt++ {
		current, err := processor.store.MarkAttemptStarted(ctx, attempt.PaymentID, nextAttempt, processor.clock.Now())
		if err != nil {
			return err
		}
		result, err := processor.rail.Charge(ctx, RailChargeRequest{
			PaymentID:           current.PaymentID,
			IdempotencyKey:      current.IdempotencyKey,
			TraceID:             current.TraceID,
			AmountCents:         current.AmountCents,
			Currency:            current.Currency,
			LedgerReservationID: current.LedgerReservationID,
			WalletDebitID:       current.WalletDebitID,
		})
		if err == nil {
			if result.CompletedAt.IsZero() {
				result.CompletedAt = processor.clock.Now()
			}
			completed, err := processor.store.MarkCompleted(ctx, current.PaymentID, result, processor.clock.Now())
			if err != nil {
				return err
			}
			return processor.publishCompleted(ctx, completed)
		}
		failure := classifyRailFailure(err)
		if !failure.Retryable {
			failed, err := processor.store.MarkFailed(ctx, current.PaymentID, failure, processor.clock.Now())
			if err != nil {
				return err
			}
			return processor.publishFailed(ctx, failed)
		}
		if nextAttempt == maxAttempts {
			failure.Retryable = false
			if failure.Code == "" {
				failure.Code = "rail_retry_exhausted"
			}
			if failure.Message == "" {
				failure.Message = "retryable rail failure exhausted"
			}
			failed, err := processor.store.MarkFailed(ctx, current.PaymentID, failure, processor.clock.Now())
			if err != nil {
				return err
			}
			return processor.publishFailed(ctx, failed)
		}
		backoff := processor.jitter(processor.backoffs[nextAttempt-1])
		if err := processor.sleeper.Sleep(ctx, backoff); err != nil {
			return err
		}
	}
	return nil
}

func (processor *Processor) publishCompleted(ctx context.Context, attempt Attempt) error {
	completedAt := attempt.CompletedAt
	if completedAt.IsZero() {
		completedAt = processor.clock.Now()
	}
	payload, err := json.Marshal(saga.PaymentCompleted{
		EventID:            "payment.completed:" + attempt.PaymentID,
		PaymentID:          attempt.PaymentID,
		IdempotencyKey:     attempt.IdempotencyKey,
		TraceID:            attempt.TraceID,
		ProcessorPaymentID: attempt.ProcessorPaymentID,
		Status:             StatusCompleted,
		CompletedAt:        completedAt,
		OccurredAt:         processor.clock.Now(),
	})
	if err != nil {
		return err
	}
	return processor.outbox.Enqueue(ctx, saga.TopicPaymentCompleted, attempt.PaymentID, payload)
}

func (processor *Processor) publishFailed(ctx context.Context, attempt Attempt) error {
	failedAt := attempt.FailedAt
	if failedAt.IsZero() {
		failedAt = processor.clock.Now()
	}
	payload, err := json.Marshal(saga.PaymentFailed{
		EventID:        "payment.failed:" + attempt.PaymentID,
		PaymentID:      attempt.PaymentID,
		IdempotencyKey: attempt.IdempotencyKey,
		TraceID:        attempt.TraceID,
		FailureCode:    attempt.FailureCode,
		FailureMessage: attempt.FailureMessage,
		FailedAt:       failedAt,
		OccurredAt:     processor.clock.Now(),
	})
	if err != nil {
		return err
	}
	return processor.outbox.Enqueue(ctx, saga.TopicPaymentFailed, attempt.PaymentID, payload)
}

func validateCommand(command saga.PaymentExecute) error {
	if command.PaymentID == "" {
		return errors.New("payment_id is required")
	}
	if command.IdempotencyKey == "" {
		return errors.New("idempotency_key is required")
	}
	if command.AmountCents <= 0 {
		return errors.New("amount_cents must be positive")
	}
	if command.Currency == "" {
		return errors.New("currency is required")
	}
	return nil
}

func classifyRailFailure(err error) RailFailure {
	var railErr RailError
	if errors.As(err, &railErr) {
		return railErr.Failure
	}
	if errors.Is(err, context.Canceled) {
		return RailFailure{Code: "context_canceled", Message: err.Error(), Retryable: false}
	}
	return RailFailure{Code: "rail_unavailable", Message: err.Error(), Retryable: true}
}

func randomJitter(duration time.Duration) time.Duration {
	if duration <= 0 {
		return 0
	}
	half := duration / 2
	return half + time.Duration(rand.Int63n(int64(duration-half)+1))
}

type contextSleeper struct{}

func (contextSleeper) Sleep(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type systemClock struct{}

func (systemClock) Now() time.Time {
	return time.Now().UTC()
}
