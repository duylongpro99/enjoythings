package saga

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"enjoythings/services/internal/telemetry"
)

// FraudReviewDecision is an operator decision on a saga held in FRAUD_REVIEW.
//
// Sagas enter FRAUD_REVIEW when the fraud worker flags them, and no event moves
// them out again: payment results that arrive during review are stored as a
// deferred terminal result instead of being applied. These decisions are the
// only exit, so every one of them is written to the fraud audit trail with the
// actor that made it.
type FraudReviewDecision struct {
	PaymentID string
	ActorID   string
	Reason    string
	TraceID   string
}

// ResumeFraudReview clears a review and lets the payment finish.
//
// A terminal result deferred during review is applied immediately, so a payment
// that the rail already completed or failed reaches its real end state instead
// of waiting for an event that will never be redelivered. With no deferred
// result the saga returns to PAYMENT_PROCESSING and the pending result is
// applied normally when it arrives.
func (orchestrator *Orchestrator) ResumeFraudReview(ctx context.Context, decision FraudReviewDecision) (Saga, error) {
	ctx, span := orchestrator.startSpan(ctx, "saga.fraud_review_resume", decision.PaymentID)
	defer span.End()

	current, err := orchestrator.store.GetByPaymentID(ctx, decision.PaymentID)
	if err != nil {
		telemetry.RecordError(span, err)
		return Saga{}, err
	}
	if current.State != StateFraudReview {
		return Saga{}, ErrNotUnderReview
	}

	now := orchestrator.clock.Now()
	deferred := current.DeferredPaymentJSON
	current.State = StatePaymentProcessing
	current.DeferredPaymentJSON = ""
	current.UpdatedAt = now

	updated, err := orchestrator.updateWithAudit(ctx, current, FraudAuditRecord{
		EventID:     reviewAuditEventID("resume", current.PaymentID, now),
		PaymentID:   current.PaymentID,
		Kind:        FraudAuditKindReviewResumed,
		SagaState:   StateFraudReview,
		DetailsJSON: reviewAuditJSON("resume", decision, StateFraudReview, StatePaymentProcessing, deferred),
		CreatedAt:   now,
	})
	if err != nil {
		telemetry.RecordError(span, err)
		return Saga{}, err
	}
	telemetry.ServiceMetrics("saga-orchestrator").RecordSagaState(StatePaymentProcessing)
	telemetry.ServiceMetrics("saga-orchestrator").RecordSaga("fraud_review_resumed", 0)

	if deferred == "" {
		return updated, nil
	}
	if err := orchestrator.applyDeferredResult(ctx, deferred, decision.TraceID); err != nil {
		telemetry.RecordError(span, err)
		return Saga{}, err
	}
	return orchestrator.store.GetByPaymentID(ctx, decision.PaymentID)
}

// RejectFraudReview refunds the payer and fails the saga.
//
// The wallet debit and any ledger reservation are compensated through the same
// path a rail failure takes, so a rejected payment is indistinguishable from a
// failed one downstream apart from its failure code. A terminal result deferred
// during review is kept on the saga: it records what the rail did, and clearing
// it would hide that from the audit trail.
func (orchestrator *Orchestrator) RejectFraudReview(ctx context.Context, decision FraudReviewDecision) (Saga, error) {
	ctx, span := orchestrator.startSpan(ctx, "saga.fraud_review_reject", decision.PaymentID)
	defer span.End()

	current, err := orchestrator.store.GetByPaymentID(ctx, decision.PaymentID)
	if err != nil {
		telemetry.RecordError(span, err)
		return Saga{}, err
	}
	if current.State != StateFraudReview {
		return Saga{}, ErrNotUnderReview
	}

	now := orchestrator.clock.Now()
	reason := strings.TrimSpace(decision.Reason)
	if reason == "" {
		reason = defaultRejectReason
	}
	current.State = StateCompensatingLedger
	current.FailureCode = FailureCodeFraudRejected
	current.LastError = reason
	current.UpdatedAt = now

	updated, err := orchestrator.updateWithAudit(ctx, current, FraudAuditRecord{
		EventID:     reviewAuditEventID("reject", current.PaymentID, now),
		PaymentID:   current.PaymentID,
		Kind:        FraudAuditKindReviewRejected,
		SagaState:   StateFraudReview,
		DetailsJSON: reviewAuditJSON("reject", decision, StateFraudReview, StateCompensatingLedger, current.DeferredPaymentJSON),
		CreatedAt:   now,
	})
	if err != nil {
		telemetry.RecordError(span, err)
		return Saga{}, err
	}
	telemetry.ServiceMetrics("saga-orchestrator").RecordSaga("fraud_review_rejected", 0)

	if err := orchestrator.resumeCompensation(ctx, updated, true); err != nil {
		telemetry.RecordError(span, err)
		return Saga{}, err
	}
	return orchestrator.store.GetByPaymentID(ctx, decision.PaymentID)
}

// applyDeferredResult replays a stored payment result through its normal
// handler. The event ID prefix written by the payment processor is what
// distinguishes a completion from a failure.
func (orchestrator *Orchestrator) applyDeferredResult(ctx context.Context, payload, fallbackTraceID string) error {
	eventID := deferredEventID(payload)
	switch {
	case strings.HasPrefix(eventID, TopicPaymentCompleted+":"):
		var completed PaymentCompleted
		if err := json.Unmarshal([]byte(payload), &completed); err != nil {
			return err
		}
		completed.TraceID = traceID(completed.TraceID, fallbackTraceID)
		return orchestrator.HandlePaymentCompleted(ctx, completed)
	case strings.HasPrefix(eventID, TopicPaymentFailed+":"):
		var failed PaymentFailed
		if err := json.Unmarshal([]byte(payload), &failed); err != nil {
			return err
		}
		failed.TraceID = traceID(failed.TraceID, fallbackTraceID)
		return orchestrator.HandlePaymentFailed(ctx, failed)
	default:
		return ErrUnknownDeferredResult
	}
}

// reviewAuditEventID keeps repeated decisions on one payment distinct, because
// the audit table deduplicates on event ID.
func reviewAuditEventID(action, paymentID string, at time.Time) string {
	return "review." + action + ":" + paymentID + ":" + at.UTC().Format(time.RFC3339Nano)
}

func reviewAuditJSON(action string, decision FraudReviewDecision, fromState, toState, deferred string) string {
	payload, _ := json.Marshal(struct {
		Action    string          `json:"action"`
		PaymentID string          `json:"payment_id"`
		ActorID   string          `json:"actor_id"`
		Reason    string          `json:"reason,omitempty"`
		FromState string          `json:"from_state"`
		ToState   string          `json:"to_state"`
		TraceID   string          `json:"trace_id,omitempty"`
		Deferred  json.RawMessage `json:"deferred_result,omitempty"`
	}{
		Action:    action,
		PaymentID: decision.PaymentID,
		ActorID:   decision.ActorID,
		Reason:    strings.TrimSpace(decision.Reason),
		FromState: fromState,
		ToState:   toState,
		TraceID:   decision.TraceID,
		Deferred:  json.RawMessage(deferred),
	})
	return string(payload)
}
