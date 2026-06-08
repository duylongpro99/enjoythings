package saga

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"enjoythings/services/internal/event"
	"enjoythings/services/internal/telemetry"

	"go.opentelemetry.io/otel/trace"
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

type OutboxRecord struct {
	Topic        string
	PartitionKey string
	Payload      []byte
}

type atomicOutboxStore interface {
	UpdateWithOutbox(context.Context, Saga, []OutboxRecord) (Saga, error)
}

type auditStore interface {
	RecordFraudAudit(context.Context, FraudAuditRecord) error
}

type auditUpdateStore interface {
	UpdateWithAudit(context.Context, Saga, FraudAuditRecord) (Saga, error)
}

type auditOutboxStore interface {
	UpdateWithOutboxAndAudit(context.Context, Saga, []OutboxRecord, FraudAuditRecord) (Saga, error)
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

func (orchestrator *Orchestrator) startSpan(ctx context.Context, name, paymentID string) (context.Context, trace.Span) {
	return telemetry.Tracer().Start(
		ctx,
		name,
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(telemetry.SafeAttributes(
			"payment.id", paymentID,
			"operation", name,
		)...),
	)
}

func (orchestrator *Orchestrator) StartPaymentSaga(ctx context.Context, req StartRequest) (Saga, error) {
	ctx, span := orchestrator.startSpan(ctx, "saga.start", req.PaymentID)
	defer span.End()
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
		telemetry.RecordError(span, err)
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
	ctx, span := orchestrator.startSpan(ctx, "saga.payment_completed", event.PaymentID)
	defer span.End()
	current, err := orchestrator.store.GetByPaymentID(ctx, event.PaymentID)
	if err != nil {
		telemetry.RecordError(span, err)
		return err
	}
	if current.State == StateCompleted {
		return orchestrator.recordFraudAudit(ctx, FraudAuditRecord{
			EventID:     event.EventID,
			PaymentID:   current.PaymentID,
			Kind:        FraudAuditKindDuplicate,
			SagaState:   current.State,
			DetailsJSON: paymentResultAuditJSON("payment.completed", event.EventID, current.State, "duplicate", event.Status, event.CompletedAt, event.OccurredAt, event.TraceID, event.ProcessorPaymentID, "", ""),
			CreatedAt:   orchestrator.clock.Now(),
		})
	}
	if current.State == StateFailed {
		return orchestrator.recordFraudAudit(ctx, FraudAuditRecord{
			EventID:     event.EventID,
			PaymentID:   current.PaymentID,
			Kind:        FraudAuditKindInvariantViolation,
			SagaState:   current.State,
			DetailsJSON: paymentResultAuditJSON("payment.completed", event.EventID, current.State, "terminal_state_mismatch", event.Status, event.CompletedAt, event.OccurredAt, event.TraceID, event.ProcessorPaymentID, "", ""),
			CreatedAt:   orchestrator.clock.Now(),
		})
	}
	if current.State == StateFraudReview {
		return orchestrator.deferTerminalPaymentResult(ctx, current, event)
	}
	confirmed, err := orchestrator.ledger.ConfirmTransfer(ctx, LedgerConfirmRequest{
		PaymentID:           current.PaymentID,
		IdempotencyKey:      stepKey(current.PaymentID, "confirm-ledger"),
		TraceID:             traceID(event.TraceID, current.TraceID),
		LedgerReservationID: current.LedgerReservationID,
		WalletDebitID:       current.WalletDebitID,
	})
	if err != nil {
		telemetry.RecordError(span, err)
		return err
	}
	now := orchestrator.clock.Now()
	current.State = StateLedgerConfirmed
	current.TransferID = confirmed.TransferID
	current.UpdatedAt = now
	current, err = orchestrator.store.Update(ctx, current)
	if err != nil {
		telemetry.RecordError(span, err)
		return err
	}
	if err := orchestrator.publishTxCompleted(ctx, current, traceID(event.TraceID, current.TraceID), nonZeroTime(confirmed.CompletedAt, now)); err != nil {
		telemetry.RecordError(span, err)
		return err
	}
	current.State = StateCompleted
	current.UpdatedAt = now
	_, err = orchestrator.store.Update(ctx, current)
	if err != nil {
		telemetry.RecordError(span, err)
	}
	return err
}

func (orchestrator *Orchestrator) HandlePaymentFailed(ctx context.Context, event PaymentFailed) error {
	ctx, span := orchestrator.startSpan(ctx, "saga.payment_failed", event.PaymentID)
	defer span.End()
	current, err := orchestrator.store.GetByPaymentID(ctx, event.PaymentID)
	if err != nil {
		telemetry.RecordError(span, err)
		return err
	}
	if current.State == StateFailed {
		return orchestrator.recordFraudAudit(ctx, FraudAuditRecord{
			EventID:     event.EventID,
			PaymentID:   current.PaymentID,
			Kind:        FraudAuditKindDuplicate,
			SagaState:   current.State,
			DetailsJSON: paymentResultAuditJSON("payment.failed", event.EventID, current.State, "duplicate", "", event.FailedAt, event.OccurredAt, event.TraceID, "", event.FailureCode, event.FailureMessage),
			CreatedAt:   orchestrator.clock.Now(),
		})
	}
	if current.State == StateCompleted {
		return orchestrator.recordFraudAudit(ctx, FraudAuditRecord{
			EventID:     event.EventID,
			PaymentID:   current.PaymentID,
			Kind:        FraudAuditKindInvariantViolation,
			SagaState:   current.State,
			DetailsJSON: paymentResultAuditJSON("payment.failed", event.EventID, current.State, "terminal_state_mismatch", "", event.FailedAt, event.OccurredAt, event.TraceID, "", event.FailureCode, event.FailureMessage),
			CreatedAt:   orchestrator.clock.Now(),
		})
	}
	if current.State == StateFraudReview {
		return orchestrator.deferTerminalPaymentResult(ctx, current, event)
	}
	now := orchestrator.clock.Now()
	current.State = StateCompensatingLedger
	current.FailureCode = event.FailureCode
	current.LastError = event.FailureMessage
	current.UpdatedAt = now
	current, err = orchestrator.store.Update(ctx, current)
	if err != nil {
		telemetry.RecordError(span, err)
		return err
	}
	if err := orchestrator.ledger.CancelReservation(ctx, LedgerCancelRequest{
		PaymentID:           current.PaymentID,
		IdempotencyKey:      stepKey(current.PaymentID, "cancel-ledger"),
		TraceID:             traceID(event.TraceID, current.TraceID),
		LedgerReservationID: current.LedgerReservationID,
		Reason:              event.FailureMessage,
	}); err != nil {
		telemetry.RecordError(span, err)
		return err
	}
	current.State = StateCompensatingWallet
	current.UpdatedAt = now
	current, err = orchestrator.store.Update(ctx, current)
	if err != nil {
		telemetry.RecordError(span, err)
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
		telemetry.RecordError(span, err)
		return err
	}
	current.State = StateFailed
	current.UpdatedAt = now
	current, err = orchestrator.store.Update(ctx, current)
	if err != nil {
		telemetry.RecordError(span, err)
		return err
	}
	err = orchestrator.publishTxFailed(ctx, current, traceID(event.TraceID, current.TraceID), nonZeroTime(event.FailedAt, now))
	if err != nil {
		telemetry.RecordError(span, err)
	}
	return err
}

func (orchestrator *Orchestrator) advance(ctx context.Context, current Saga) (Saga, error) {
	ctx, span := orchestrator.startSpan(ctx, "saga.advance", current.PaymentID)
	defer span.End()
	span.SetAttributes(telemetry.SafeAttributes("outcome", current.State)...)
	if current.State == StateStarted {
		verification, err := orchestrator.verification.GetStatus(ctx, VerificationRequest{
			UserID:  current.UserID,
			TraceID: current.TraceID,
		})
		if err != nil {
			if errors.Is(err, ErrVerificationNotFound) {
				return orchestrator.failUnverified(ctx, current)
			}
			return Saga{}, err
		}
		if verification.Status != VerificationVerified {
			return orchestrator.failUnverified(ctx, current)
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
		now := orchestrator.clock.Now()
		paymentPayload, err := json.Marshal(PaymentExecute{
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
			OccurredAt:          now,
		})
		if err != nil {
			return Saga{}, err
		}
		fraudPayload, err := json.Marshal(event.FraudScoreRequested{
			SchemaVersion: 1,
			EventID:       event.FraudScoreRequestedEventID(current.PaymentID),
			PaymentID:     current.PaymentID,
			UserID:        current.UserID,
			FromWalletID:  current.FromWalletID,
			ToWalletID:    current.ToWalletID,
			AmountCents:   current.AmountCents,
			Currency:      current.Currency,
			OccurredAt:    now,
			TraceID:       current.TraceID,
		})
		if err != nil {
			return Saga{}, err
		}
		current.State = StatePaymentProcessing
		current.UpdatedAt = now
		updated, err := orchestrator.updateWithOutbox(ctx, current, []OutboxRecord{
			{Topic: TopicPaymentExecute, PartitionKey: current.PaymentID, Payload: paymentPayload},
			{Topic: event.FraudScoreRequestedTopic, PartitionKey: current.PaymentID, Payload: fraudPayload},
		})
		if err != nil {
			return Saga{}, err
		}
		current = updated
	}
	return current, nil
}

func (orchestrator *Orchestrator) updateWithOutbox(ctx context.Context, current Saga, events []OutboxRecord) (Saga, error) {
	ctx, span := orchestrator.startSpan(ctx, "saga.outbox", current.PaymentID)
	defer span.End()
	if store, ok := orchestrator.store.(atomicOutboxStore); ok {
		updated, err := store.UpdateWithOutbox(ctx, current, events)
		if err != nil {
			telemetry.RecordError(span, err)
		}
		return updated, err
	}
	for _, record := range events {
		if err := orchestrator.outbox.Enqueue(ctx, record.Topic, record.PartitionKey, record.Payload); err != nil {
			telemetry.RecordError(span, err)
			return Saga{}, err
		}
	}
	updated, err := orchestrator.store.Update(ctx, current)
	if err != nil {
		telemetry.RecordError(span, err)
	}
	return updated, err
}

func (orchestrator *Orchestrator) updateWithAudit(ctx context.Context, current Saga, audit FraudAuditRecord) (Saga, error) {
	if store, ok := orchestrator.store.(auditUpdateStore); ok {
		return store.UpdateWithAudit(ctx, current, audit)
	}
	updated, err := orchestrator.store.Update(ctx, current)
	if err != nil {
		return Saga{}, err
	}
	if err := orchestrator.recordFraudAudit(ctx, audit); err != nil {
		return Saga{}, err
	}
	return updated, nil
}

func (orchestrator *Orchestrator) updateWithOutboxAndAudit(ctx context.Context, current Saga, events []OutboxRecord, audit FraudAuditRecord) (Saga, error) {
	ctx, span := orchestrator.startSpan(ctx, "saga.outbox_audit", current.PaymentID)
	defer span.End()
	if store, ok := orchestrator.store.(auditOutboxStore); ok {
		updated, err := store.UpdateWithOutboxAndAudit(ctx, current, events, audit)
		if err != nil {
			telemetry.RecordError(span, err)
		}
		return updated, err
	}
	for _, record := range events {
		if err := orchestrator.outbox.Enqueue(ctx, record.Topic, record.PartitionKey, record.Payload); err != nil {
			telemetry.RecordError(span, err)
			return Saga{}, err
		}
	}
	updated, err := orchestrator.store.Update(ctx, current)
	if err != nil {
		telemetry.RecordError(span, err)
		return Saga{}, err
	}
	if err := orchestrator.recordFraudAudit(ctx, audit); err != nil {
		telemetry.RecordError(span, err)
		return Saga{}, err
	}
	return updated, nil
}

func (orchestrator *Orchestrator) recordFraudAudit(ctx context.Context, audit FraudAuditRecord) error {
	if store, ok := orchestrator.store.(auditStore); ok {
		return store.RecordFraudAudit(ctx, audit)
	}
	return nil
}

func (orchestrator *Orchestrator) HandleFraudFlagged(ctx context.Context, flagged event.FraudFlagged) error {
	ctx, span := orchestrator.startSpan(ctx, "saga.fraud_flagged", flagged.PaymentID)
	defer span.End()
	span.SetAttributes(telemetry.SafeAttributes("verdict.action", flagged.Action)...)
	if err := flagged.Validate(); err != nil {
		telemetry.RecordError(span, err)
		return err
	}
	current, err := orchestrator.store.GetByPaymentID(ctx, flagged.PaymentID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return orchestrator.recordFraudAudit(ctx, FraudAuditRecord{
				EventID:     flagged.EventID,
				PaymentID:   flagged.PaymentID,
				Kind:        FraudAuditKindOrphan,
				SagaState:   "",
				DetailsJSON: fraudFlaggedAuditJSON(flagged, "", FraudAuditKindOrphan),
				CreatedAt:   orchestrator.clock.Now(),
			})
		}
		telemetry.RecordError(span, err)
		return err
	}
	now := orchestrator.clock.Now()
	current.FraudSessionID = flagged.SessionID
	current.FraudAction = flagged.Action
	current.FraudRiskScore = flagged.RiskScore
	current.FraudReason = flagged.Reason
	current.FraudFlaggedAt = nonZeroTime(flagged.OccurredAt, now)
	audit := FraudAuditRecord{
		EventID:     flagged.EventID,
		PaymentID:   current.PaymentID,
		Kind:        fraudFlaggedAuditKind(current.State),
		SagaState:   current.State,
		DetailsJSON: fraudFlaggedAuditJSON(flagged, current.State, fraudFlaggedAuditKind(current.State)),
		CreatedAt:   now,
	}
	if current.State == StatePaymentProcessing {
		current.State = StateFraudReview
		current.UpdatedAt = now
		pausedPayload, err := json.Marshal(event.TxPaused{
			SchemaVersion: 1,
			EventID:       event.TxPausedEventID(current.PaymentID),
			PaymentID:     current.PaymentID,
			SessionID:     current.FraudSessionID,
			Action:        current.FraudAction,
			RiskScore:     current.FraudRiskScore,
			Reason:        current.FraudReason,
			PausedAt:      now,
			OccurredAt:    now,
			TraceID:       traceID(flagged.TraceID, current.TraceID),
		})
		if err != nil {
			return err
		}
		_, err = orchestrator.updateWithOutboxAndAudit(ctx, current, []OutboxRecord{
			{Topic: event.TxPausedTopic, PartitionKey: current.PaymentID, Payload: pausedPayload},
		}, audit)
		return err
	}
	current.UpdatedAt = now
	_, err = orchestrator.updateWithAudit(ctx, current, audit)
	return err
}

func (orchestrator *Orchestrator) deferTerminalPaymentResult(ctx context.Context, current Saga, terminal any) error {
	switch event := terminal.(type) {
	case PaymentCompleted:
		return orchestrator.deferPaymentCompleted(ctx, current, event)
	case PaymentFailed:
		return orchestrator.deferPaymentFailed(ctx, current, event)
	default:
		return fmt.Errorf("unsupported terminal event %T", terminal)
	}
}

func (orchestrator *Orchestrator) deferPaymentCompleted(ctx context.Context, current Saga, event PaymentCompleted) error {
	if current.DeferredPaymentJSON != "" {
		if deferredEventID(current.DeferredPaymentJSON) == event.EventID {
			return orchestrator.recordFraudAudit(ctx, FraudAuditRecord{
				EventID:     event.EventID,
				PaymentID:   current.PaymentID,
				Kind:        FraudAuditKindDuplicate,
				SagaState:   current.State,
				DetailsJSON: paymentResultAuditJSON("payment.completed", event.EventID, current.State, "duplicate", event.Status, event.CompletedAt, event.OccurredAt, event.TraceID, event.ProcessorPaymentID, "", ""),
				CreatedAt:   orchestrator.clock.Now(),
			})
		}
		return orchestrator.recordFraudAudit(ctx, FraudAuditRecord{
			EventID:     event.EventID,
			PaymentID:   current.PaymentID,
			Kind:        FraudAuditKindInvariantViolation,
			SagaState:   current.State,
			DetailsJSON: paymentResultAuditJSON("payment.completed", event.EventID, current.State, "conflicting_terminal_result", event.Status, event.CompletedAt, event.OccurredAt, event.TraceID, event.ProcessorPaymentID, "", ""),
			CreatedAt:   orchestrator.clock.Now(),
		})
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	current.DeferredPaymentJSON = string(payload)
	current.UpdatedAt = orchestrator.clock.Now()
	_, err = orchestrator.updateWithAudit(ctx, current, FraudAuditRecord{
		EventID:     event.EventID,
		PaymentID:   current.PaymentID,
		Kind:        FraudAuditKindDeferredTerminal,
		SagaState:   current.State,
		DetailsJSON: paymentResultAuditJSON("payment.completed", event.EventID, current.State, "deferred", event.Status, event.CompletedAt, event.OccurredAt, event.TraceID, event.ProcessorPaymentID, "", ""),
		CreatedAt:   current.UpdatedAt,
	})
	return err
}

func (orchestrator *Orchestrator) deferPaymentFailed(ctx context.Context, current Saga, event PaymentFailed) error {
	if current.DeferredPaymentJSON != "" {
		if deferredEventID(current.DeferredPaymentJSON) == event.EventID {
			return orchestrator.recordFraudAudit(ctx, FraudAuditRecord{
				EventID:     event.EventID,
				PaymentID:   current.PaymentID,
				Kind:        FraudAuditKindDuplicate,
				SagaState:   current.State,
				DetailsJSON: paymentResultAuditJSON("payment.failed", event.EventID, current.State, "duplicate", "", event.FailedAt, event.OccurredAt, event.TraceID, "", event.FailureCode, event.FailureMessage),
				CreatedAt:   orchestrator.clock.Now(),
			})
		}
		return orchestrator.recordFraudAudit(ctx, FraudAuditRecord{
			EventID:     event.EventID,
			PaymentID:   current.PaymentID,
			Kind:        FraudAuditKindInvariantViolation,
			SagaState:   current.State,
			DetailsJSON: paymentResultAuditJSON("payment.failed", event.EventID, current.State, "conflicting_terminal_result", "", event.FailedAt, event.OccurredAt, event.TraceID, "", event.FailureCode, event.FailureMessage),
			CreatedAt:   orchestrator.clock.Now(),
		})
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return err
	}
	current.DeferredPaymentJSON = string(payload)
	current.UpdatedAt = orchestrator.clock.Now()
	_, err = orchestrator.updateWithAudit(ctx, current, FraudAuditRecord{
		EventID:     event.EventID,
		PaymentID:   current.PaymentID,
		Kind:        FraudAuditKindDeferredTerminal,
		SagaState:   current.State,
		DetailsJSON: paymentResultAuditJSON("payment.failed", event.EventID, current.State, "deferred", "", event.FailedAt, event.OccurredAt, event.TraceID, "", event.FailureCode, event.FailureMessage),
		CreatedAt:   current.UpdatedAt,
	})
	return err
}

func fraudFlaggedAuditKind(state string) string {
	switch state {
	case StatePaymentProcessing:
		return FraudAuditKindTransition
	case StateFraudReview, StateCompleted, StateFailed:
		return FraudAuditKindDuplicate
	default:
		return FraudAuditKindIgnored
	}
}

func fraudFlaggedAuditJSON(flagged event.FraudFlagged, state, kind string) string {
	payload, _ := json.Marshal(struct {
		SourceEventID string  `json:"source_event_id"`
		PaymentID     string  `json:"payment_id"`
		State         string  `json:"state"`
		Kind          string  `json:"kind"`
		SessionID     string  `json:"session_id"`
		Action        string  `json:"action"`
		RiskScore     float64 `json:"risk_score"`
		Reason        string  `json:"reason"`
	}{
		SourceEventID: flagged.SourceEventID,
		PaymentID:     flagged.PaymentID,
		State:         state,
		Kind:          kind,
		SessionID:     flagged.SessionID,
		Action:        flagged.Action,
		RiskScore:     flagged.RiskScore,
		Reason:        flagged.Reason,
	})
	return string(payload)
}

func paymentResultAuditJSON(topic, eventID, state, outcome string, status string, timestamp time.Time, occurredAt time.Time, traceID, processorPaymentID, failureCode, failureMessage string) string {
	payload, _ := json.Marshal(struct {
		Topic              string    `json:"topic"`
		EventID            string    `json:"event_id"`
		State              string    `json:"state"`
		Outcome            string    `json:"outcome"`
		Status             string    `json:"status,omitempty"`
		Timestamp          time.Time `json:"timestamp,omitempty"`
		OccurredAt         time.Time `json:"occurred_at,omitempty"`
		TraceID            string    `json:"trace_id"`
		ProcessorPaymentID string    `json:"processor_payment_id,omitempty"`
		FailureCode        string    `json:"failure_code,omitempty"`
		FailureMessage     string    `json:"failure_message,omitempty"`
	}{
		Topic:              topic,
		EventID:            eventID,
		State:              state,
		Outcome:            outcome,
		Status:             status,
		Timestamp:          timestamp,
		OccurredAt:         occurredAt,
		TraceID:            traceID,
		ProcessorPaymentID: processorPaymentID,
		FailureCode:        failureCode,
		FailureMessage:     failureMessage,
	})
	return string(payload)
}

func deferredEventID(payload string) string {
	var envelope struct {
		EventID string `json:"event_id"`
	}
	if err := json.Unmarshal([]byte(payload), &envelope); err != nil {
		return ""
	}
	return envelope.EventID
}

func (orchestrator *Orchestrator) failUnverified(ctx context.Context, current Saga) (Saga, error) {
	current.State = StateFailed
	current.LastError = ErrUnverified.Error()
	current.UpdatedAt = orchestrator.clock.Now()
	updated, err := orchestrator.store.Update(ctx, current)
	if err != nil {
		return Saga{}, err
	}
	return updated, ErrUnverified
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

func (orchestrator *Orchestrator) publishTxPaused(ctx context.Context, current Saga, traceID string, pausedAt time.Time) error {
	payload, err := json.Marshal(event.TxPaused{
		SchemaVersion: 1,
		EventID:       event.TxPausedEventID(current.PaymentID),
		PaymentID:     current.PaymentID,
		SessionID:     current.FraudSessionID,
		Action:        current.FraudAction,
		RiskScore:     current.FraudRiskScore,
		Reason:        current.FraudReason,
		PausedAt:      pausedAt,
		OccurredAt:    orchestrator.clock.Now(),
		TraceID:       traceID,
	})
	if err != nil {
		return err
	}
	return orchestrator.outbox.Enqueue(ctx, event.TxPausedTopic, current.PaymentID, payload)
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
