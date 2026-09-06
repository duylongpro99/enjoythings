package saga

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"enjoythings/services/internal/telemetry"
)

// ReviewReaperActorID names the system actor on the audit record written when
// a review is rejected because its deadline passed, so an expiry stays
// distinguishable from a human decision in the trail.
const ReviewReaperActorID = "system:fraud-review-reaper"

// ExpireFraudReviews rejects every saga held in FRAUD_REVIEW for longer than
// ttl and reports how many it rejected.
//
// A review that nobody decides otherwise holds the payer's money forever. The
// reaper closes that hole with the same RejectFraudReview path an operator
// uses, so an expired review is refunded, failed with fraud_rejected, and
// audited exactly like a manual rejection; only the actor differs. A saga that
// leaves review between the listing and the decision is skipped, not reported.
func (orchestrator *Orchestrator) ExpireFraudReviews(ctx context.Context, ttl time.Duration) (int, error) {
	if ttl <= 0 {
		return 0, errors.New("fraud review ttl must be positive")
	}
	sagas, err := orchestrator.store.ListNonTerminal(ctx)
	if err != nil {
		return 0, err
	}
	deadline := orchestrator.clock.Now().Add(-ttl)
	expired := 0
	var failures []error
	for _, current := range sagas {
		if current.State != StateFraudReview || reviewStartedAt(current).After(deadline) {
			continue
		}
		_, err := orchestrator.RejectFraudReview(ctx, FraudReviewDecision{
			PaymentID: current.PaymentID,
			ActorID:   ReviewReaperActorID,
			Reason:    fmt.Sprintf("fraud review deadline of %s exceeded", ttl),
			TraceID:   current.TraceID,
		})
		switch {
		case errors.Is(err, ErrNotUnderReview):
			continue
		case err != nil:
			failures = append(failures, fmt.Errorf("payment %s: %w", current.PaymentID, err))
			continue
		}
		telemetry.ServiceMetrics("saga-orchestrator").RecordSaga("fraud_review_expired", 0)
		expired++
	}
	return expired, errors.Join(failures...)
}

// RunReviewReaper expires overdue reviews every interval until ctx ends.
func (orchestrator *Orchestrator) RunReviewReaper(ctx context.Context, interval, ttl time.Duration, logger *slog.Logger) {
	if logger == nil {
		logger = slog.Default()
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			expired, err := orchestrator.ExpireFraudReviews(ctx, ttl)
			if expired > 0 {
				logger.Info("fraud review reaper rejected overdue reviews", "count", expired, "ttl", ttl)
			}
			if err != nil && ctx.Err() == nil {
				logger.Error("fraud review reaper failed", "error", err)
			}
		}
	}
}

// reviewStartedAt is when the saga entered review. The fraud flag timestamp is
// authoritative; rows written before it existed fall back to the last update,
// which for a saga still in review is the transition into it.
func reviewStartedAt(current Saga) time.Time {
	if !current.FraudFlaggedAt.IsZero() {
		return current.FraudFlaggedAt
	}
	return current.UpdatedAt
}
