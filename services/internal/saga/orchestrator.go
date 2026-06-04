package saga

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

type Store interface {
	Create(context.Context, Saga) (Saga, error)
	GetByPaymentID(context.Context, string) (Saga, error)
	ListNonTerminal(context.Context) ([]Saga, error)
	Update(context.Context, Saga) (Saga, error)
}

type VerificationClient interface {
	GetStatus(context.Context, VerificationRequest) (VerificationResult, error)
}

type WalletClient interface {
	DebitForSaga(context.Context, WalletDebitRequest) (WalletDebitResult, error)
	CompensateDebit(context.Context, WalletCompensateRequest) error
}

type LedgerClient interface {
	ReserveTransfer(context.Context, LedgerReserveRequest) (LedgerReserveResult, error)
	ConfirmTransfer(context.Context, LedgerConfirmRequest) (LedgerConfirmResult, error)
	CancelReservation(context.Context, LedgerCancelRequest) error
}

type Outbox interface {
	Enqueue(context.Context, string, string, []byte) error
}

type Clock interface {
	Now() time.Time
}

type Orchestrator struct {
	store        Store
	verification VerificationClient
	wallet       WalletClient
	ledger       LedgerClient
	outbox       Outbox
	clock        Clock
}

func NewOrchestrator(store Store, verification VerificationClient, wallet WalletClient, ledger LedgerClient, clock Clock) *Orchestrator {
	if clock == nil {
		clock = systemClock{}
	}
	return &Orchestrator{
		store:        store,
		verification: verification,
		wallet:       wallet,
		ledger:       ledger,
		outbox:       noopOutbox{},
		clock:        clock,
	}
}

func (orchestrator *Orchestrator) SetOutbox(outbox Outbox) {
	if outbox != nil {
		orchestrator.outbox = outbox
	}
}

func (orchestrator *Orchestrator) StartPaymentSaga(ctx context.Context, req StartRequest) (Saga, error) {
	now := orchestrator.clock.Now()
	created, err := orchestrator.store.Create(ctx, Saga{
		PaymentID:      req.PaymentID,
		IdempotencyKey: req.IdempotencyKey,
		UserID:         req.UserID,
		FromWalletID:   req.FromWalletID,
		ToWalletID:     req.ToWalletID,
		AmountCents:    req.AmountCents,
		Currency:       req.Currency,
		State:          StateStarted,
		TraceID:        req.TraceID,
		CreatedAt:      now,
		UpdatedAt:      now,
	})
	if err != nil {
		return Saga{}, err
	}
	if created.State != StateStarted {
		return created, nil
	}
	return orchestrator.advance(ctx, created)
}

func (orchestrator *Orchestrator) GetPaymentSaga(ctx context.Context, paymentID string) (Saga, error) {
	return orchestrator.store.GetByPaymentID(ctx, paymentID)
}

func (orchestrator *Orchestrator) ResumeNonTerminal(ctx context.Context) error {
	sagas, err := orchestrator.store.ListNonTerminal(ctx)
	if err != nil {
		return err
	}
	for _, current := range sagas {
		if err := orchestrator.resumeOne(ctx, current); err != nil {
			return err
		}
	}
	return nil
}

func (orchestrator *Orchestrator) HandlePaymentCompleted(ctx context.Context, event PaymentCompleted) error {
	current, err := orchestrator.store.GetByPaymentID(ctx, event.PaymentID)
	if err != nil {
		return err
	}
	if current.State == StateCompleted {
		return nil
	}
	confirmed, err := orchestrator.ledger.ConfirmTransfer(ctx, LedgerConfirmRequest{
		PaymentID:           current.PaymentID,
		IdempotencyKey:      stepKey(current.PaymentID, "confirm-ledger"),
		TraceID:             traceID(event.TraceID, current.TraceID),
		LedgerReservationID: current.LedgerReservationID,
		WalletDebitID:       current.WalletDebitID,
	})
	if err != nil {
		return err
	}
	now := orchestrator.clock.Now()
	current.State = StateLedgerConfirmed
	current.TransferID = confirmed.TransferID
	current.UpdatedAt = now
	current, err = orchestrator.store.Update(ctx, current)
	if err != nil {
		return err
	}
	if err := orchestrator.publishTxCompleted(ctx, current, traceID(event.TraceID, current.TraceID), nonZeroTime(confirmed.CompletedAt, now)); err != nil {
		return err
	}
	current.State = StateCompleted
	current.UpdatedAt = now
	_, err = orchestrator.store.Update(ctx, current)
	return err
}

func (orchestrator *Orchestrator) HandlePaymentFailed(ctx context.Context, event PaymentFailed) error {
	current, err := orchestrator.store.GetByPaymentID(ctx, event.PaymentID)
	if err != nil {
		return err
	}
	if current.State == StateFailed {
		return nil
	}
	now := orchestrator.clock.Now()
	current.State = StateCompensatingLedger
	current.FailureCode = event.FailureCode
	current.LastError = event.FailureMessage
	current.UpdatedAt = now
	current, err = orchestrator.store.Update(ctx, current)
	if err != nil {
		return err
	}
	if err := orchestrator.ledger.CancelReservation(ctx, LedgerCancelRequest{
		PaymentID:           current.PaymentID,
		IdempotencyKey:      stepKey(current.PaymentID, "cancel-ledger"),
		TraceID:             traceID(event.TraceID, current.TraceID),
		LedgerReservationID: current.LedgerReservationID,
		Reason:              event.FailureMessage,
	}); err != nil {
		return err
	}
	current.State = StateCompensatingWallet
	current.UpdatedAt = now
	current, err = orchestrator.store.Update(ctx, current)
	if err != nil {
		return err
	}
	if err := orchestrator.wallet.CompensateDebit(ctx, WalletCompensateRequest{
		PaymentID:      current.PaymentID,
		IdempotencyKey: stepKey(current.PaymentID, "compensate-wallet"),
		TraceID:        traceID(event.TraceID, current.TraceID),
		FromWalletID:   current.FromWalletID,
		WalletDebitID:  current.WalletDebitID,
		AmountCents:    current.AmountCents,
		Currency:       current.Currency,
		Reason:         event.FailureMessage,
	}); err != nil {
		return err
	}
	current.State = StateFailed
	current.UpdatedAt = now
	current, err = orchestrator.store.Update(ctx, current)
	if err != nil {
		return err
	}
	return orchestrator.publishTxFailed(ctx, current, traceID(event.TraceID, current.TraceID), nonZeroTime(event.FailedAt, now))
}

func (orchestrator *Orchestrator) advance(ctx context.Context, current Saga) (Saga, error) {
	if current.State == StateStarted {
		verification, err := orchestrator.verification.GetStatus(ctx, VerificationRequest{
			UserID:  current.UserID,
			TraceID: current.TraceID,
		})
		if err != nil {
			return Saga{}, err
		}
		if verification.Status != VerificationVerified {
			current.State = StateFailed
			current.LastError = ErrUnverified.Error()
			current.UpdatedAt = orchestrator.clock.Now()
			updated, updateErr := orchestrator.store.Update(ctx, current)
			if updateErr != nil {
				return Saga{}, updateErr
			}
			return updated, ErrUnverified
		}
		current.State = StateVerificationChecked
		current.UpdatedAt = orchestrator.clock.Now()
		updated, err := orchestrator.store.Update(ctx, current)
		if err != nil {
			return Saga{}, err
		}
		current = updated
	}
	if current.State == StateVerificationChecked {
		debit, err := orchestrator.wallet.DebitForSaga(ctx, WalletDebitRequest{
			PaymentID:      current.PaymentID,
			IdempotencyKey: stepKey(current.PaymentID, "debit-wallet"),
			TraceID:        current.TraceID,
			FromWalletID:   current.FromWalletID,
			AmountCents:    current.AmountCents,
			Currency:       current.Currency,
		})
		if err != nil {
			return Saga{}, err
		}
		current.State = StateWalletDebited
		current.WalletDebitID = debit.WalletDebitID
		current.UpdatedAt = orchestrator.clock.Now()
		updated, err := orchestrator.store.Update(ctx, current)
		if err != nil {
			return Saga{}, err
		}
		current = updated
	}
	if current.State == StateWalletDebited {
		reservation, err := orchestrator.ledger.ReserveTransfer(ctx, LedgerReserveRequest{
			PaymentID:      current.PaymentID,
			IdempotencyKey: stepKey(current.PaymentID, "reserve-ledger"),
			TraceID:        current.TraceID,
			FromWalletID:   current.FromWalletID,
			ToWalletID:     current.ToWalletID,
			AmountCents:    current.AmountCents,
			Currency:       current.Currency,
		})
		if err != nil {
			return Saga{}, err
		}
		current.State = StateLedgerReserved
		current.LedgerReservationID = reservation.LedgerReservationID
		current.UpdatedAt = orchestrator.clock.Now()
		updated, err := orchestrator.store.Update(ctx, current)
		if err != nil {
			return Saga{}, err
		}
		current = updated
	}
	if current.State == StateLedgerReserved {
		payload, err := json.Marshal(PaymentExecute{
			EventID:             "payment.execute:" + current.PaymentID,
			PaymentID:           current.PaymentID,
			IdempotencyKey:      stepKey(current.PaymentID, "execute-payment"),
			TraceID:             current.TraceID,
			FromWalletID:        current.FromWalletID,
			ToWalletID:          current.ToWalletID,
			AmountCents:         current.AmountCents,
			Currency:            current.Currency,
			LedgerReservationID: current.LedgerReservationID,
			WalletDebitID:       current.WalletDebitID,
			OccurredAt:          orchestrator.clock.Now(),
		})
		if err != nil {
			return Saga{}, err
		}
		if err := orchestrator.outbox.Enqueue(ctx, TopicPaymentExecute, current.PaymentID, payload); err != nil {
			return Saga{}, err
		}
		current.State = StatePaymentProcessing
		current.UpdatedAt = orchestrator.clock.Now()
		updated, err := orchestrator.store.Update(ctx, current)
		if err != nil {
			return Saga{}, err
		}
		current = updated
	}
	return current, nil
}

func (orchestrator *Orchestrator) resumeOne(ctx context.Context, current Saga) error {
	switch current.State {
	case StateLedgerConfirmed:
		if err := orchestrator.publishTxCompleted(ctx, current, current.TraceID, orchestrator.clock.Now()); err != nil {
			return err
		}
		current.State = StateCompleted
		current.UpdatedAt = orchestrator.clock.Now()
		_, err := orchestrator.store.Update(ctx, current)
		return err
	case StateCompensatingLedger:
		return orchestrator.resumeCompensation(ctx, current, true)
	case StateCompensatingWallet:
		return orchestrator.resumeCompensation(ctx, current, false)
	case StatePaymentProcessing:
		return nil
	default:
		_, err := orchestrator.advance(ctx, current)
		return err
	}
}

func (orchestrator *Orchestrator) resumeCompensation(ctx context.Context, current Saga, cancelLedger bool) error {
	if cancelLedger {
		if err := orchestrator.ledger.CancelReservation(ctx, LedgerCancelRequest{
			PaymentID:           current.PaymentID,
			IdempotencyKey:      stepKey(current.PaymentID, "cancel-ledger"),
			TraceID:             current.TraceID,
			LedgerReservationID: current.LedgerReservationID,
			Reason:              current.LastError,
		}); err != nil {
			return err
		}
		current.State = StateCompensatingWallet
		current.UpdatedAt = orchestrator.clock.Now()
		updated, err := orchestrator.store.Update(ctx, current)
		if err != nil {
			return err
		}
		current = updated
	}
	if err := orchestrator.wallet.CompensateDebit(ctx, WalletCompensateRequest{
		PaymentID:      current.PaymentID,
		IdempotencyKey: stepKey(current.PaymentID, "compensate-wallet"),
		TraceID:        current.TraceID,
		FromWalletID:   current.FromWalletID,
		WalletDebitID:  current.WalletDebitID,
		AmountCents:    current.AmountCents,
		Currency:       current.Currency,
		Reason:         current.LastError,
	}); err != nil {
		return err
	}
	current.State = StateFailed
	current.UpdatedAt = orchestrator.clock.Now()
	updated, err := orchestrator.store.Update(ctx, current)
	if err != nil {
		return err
	}
	return orchestrator.publishTxFailed(ctx, updated, updated.TraceID, orchestrator.clock.Now())
}

func (orchestrator *Orchestrator) publishTxCompleted(ctx context.Context, current Saga, traceID string, completedAt time.Time) error {
	payload, err := json.Marshal(TxCompleted{
		EventID:      "tx.completed:" + current.PaymentID,
		PaymentID:    current.PaymentID,
		TraceID:      traceID,
		FromWalletID: current.FromWalletID,
		ToWalletID:   current.ToWalletID,
		AmountCents:  current.AmountCents,
		Currency:     current.Currency,
		TransferID:   current.TransferID,
		CompletedAt:  completedAt,
		OccurredAt:   orchestrator.clock.Now(),
	})
	if err != nil {
		return err
	}
	return orchestrator.outbox.Enqueue(ctx, TopicTxCompleted, current.FromWalletID, payload)
}

func (orchestrator *Orchestrator) publishTxFailed(ctx context.Context, current Saga, traceID string, failedAt time.Time) error {
	payload, err := json.Marshal(TxFailed{
		EventID:        "tx.failed:" + current.PaymentID,
		PaymentID:      current.PaymentID,
		TraceID:        traceID,
		FromWalletID:   current.FromWalletID,
		ToWalletID:     current.ToWalletID,
		AmountCents:    current.AmountCents,
		Currency:       current.Currency,
		FailureCode:    current.FailureCode,
		FailureMessage: current.LastError,
		FailedAt:       failedAt,
		OccurredAt:     orchestrator.clock.Now(),
	})
	if err != nil {
		return err
	}
	return orchestrator.outbox.Enqueue(ctx, TopicTxFailed, current.FromWalletID, payload)
}

func stepKey(paymentID, step string) string {
	return fmt.Sprintf("%s:%s", paymentID, step)
}

func traceID(preferred, fallback string) string {
	if preferred != "" {
		return preferred
	}
	return fallback
}

func nonZeroTime(value, fallback time.Time) time.Time {
	if value.IsZero() {
		return fallback
	}
	return value
}

type systemClock struct{}

func (systemClock) Now() time.Time {
	return time.Now().UTC()
}

type noopOutbox struct{}

func (noopOutbox) Enqueue(context.Context, string, string, []byte) error {
	return nil
}
