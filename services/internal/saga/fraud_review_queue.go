package saga

import "context"

// FraudReview is what an operator reads before deciding: the saga as it stands
// and every fraud audit record written for its payment, oldest first. The
// trail carries the verdict that flagged the payment, any rail result deferred
// during the review, and the decisions already taken on it.
type FraudReview struct {
	Saga  Saga
	Audit []FraudAuditRecord
}

// ListFraudReviews is the review queue: every saga held in FRAUD_REVIEW,
// longest-held first, so the payment whose payer has waited longest is at the
// top.
func (orchestrator *Orchestrator) ListFraudReviews(ctx context.Context) ([]Saga, error) {
	return orchestrator.store.ListFraudReview(ctx)
}

// GetFraudReview returns one payment's saga with its fraud audit trail. It is
// not limited to sagas still under review: the trail is how a past decision is
// explained, so a payment whose review already ended stays readable.
func (orchestrator *Orchestrator) GetFraudReview(ctx context.Context, paymentID string) (FraudReview, error) {
	current, err := orchestrator.store.GetByPaymentID(ctx, paymentID)
	if err != nil {
		return FraudReview{}, err
	}
	audit, err := orchestrator.listFraudAudit(ctx, paymentID)
	if err != nil {
		return FraudReview{}, err
	}
	return FraudReview{Saga: current, Audit: audit}, nil
}

// listFraudAudit mirrors recordFraudAudit: a store that cannot persist the
// trail has nothing to read back.
func (orchestrator *Orchestrator) listFraudAudit(ctx context.Context, paymentID string) ([]FraudAuditRecord, error) {
	if store, ok := orchestrator.store.(auditTrailStore); ok {
		return store.ListFraudAudit(ctx, paymentID)
	}
	return nil, nil
}
