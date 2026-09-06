package sagagrpc

import (
	"context"
	"testing"

	sagav1 "enjoythings/services/gen/saga/v1"
	"enjoythings/services/internal/saga"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestListFraudReviewsMapsHeldSagas(t *testing.T) {
	app := &fakeApp{queue: []saga.Saga{{
		PaymentID:           "payment-1",
		State:               saga.StateFraudReview,
		UserID:              "user-1",
		AmountCents:         1250,
		Currency:            "USD",
		FraudSessionID:      "session-1",
		FraudAction:         "block",
		FraudRiskScore:      0.95,
		FraudReason:         "high velocity",
		FraudFlaggedAt:      fixedTime,
		DeferredPaymentJSON: `{"event_id":"payment.completed:payment-1"}`,
		CreatedAt:           fixedTime,
		UpdatedAt:           fixedTime,
	}}}
	server := NewServer(app)

	resp, err := server.ListFraudReviews(context.Background(), &sagav1.ListFraudReviewsRequest{})
	if err != nil {
		t.Fatalf("ListFraudReviews: %v", err)
	}
	if len(resp.GetSagas()) != 1 {
		t.Fatalf("sagas = %+v, want one", resp.GetSagas())
	}
	got := resp.GetSagas()[0]
	if got.GetPaymentId() != "payment-1" || got.GetStatus() != saga.StateFraudReview || got.GetUserId() != "user-1" || got.GetAmountCents() != 1250 {
		t.Fatalf("saga = %+v, want the held payment", got)
	}
	if got.GetFraudSessionId() != "session-1" || got.GetFraudAction() != "block" || got.GetFraudRiskScore() != 0.95 ||
		got.GetFraudReason() != "high velocity" || !got.GetFraudFlaggedAt().AsTime().Equal(fixedTime) {
		t.Fatalf("saga verdict = %+v, want the fraud fields mapped", got)
	}
	if got.GetDeferredPaymentJson() != `{"event_id":"payment.completed:payment-1"}` {
		t.Fatalf("deferred result = %q, want the stored rail result", got.GetDeferredPaymentJson())
	}
}

func TestGetFraudReviewMapsSagaAndAuditTrail(t *testing.T) {
	app := &fakeApp{review: saga.FraudReview{
		Saga: saga.Saga{PaymentID: "payment-1", State: saga.StateFailed, FailureCode: saga.FailureCodeFraudRejected, CreatedAt: fixedTime, UpdatedAt: fixedTime},
		Audit: []saga.FraudAuditRecord{{
			EventID:     "review.reject:payment-1",
			PaymentID:   "payment-1",
			Kind:        saga.FraudAuditKindReviewRejected,
			SagaState:   saga.StateFraudReview,
			DetailsJSON: `{"actor_id":"operator-1"}`,
			CreatedAt:   fixedTime,
		}},
	}}
	server := NewServer(app)

	resp, err := server.GetFraudReview(context.Background(), &sagav1.GetFraudReviewRequest{PaymentId: "payment-1", TraceId: "trace-1"})
	if err != nil {
		t.Fatalf("GetFraudReview: %v", err)
	}
	if app.reviewPaymentID != "payment-1" {
		t.Fatalf("app was asked for %q, want payment-1", app.reviewPaymentID)
	}
	if resp.GetSaga().GetStatus() != saga.StateFailed || resp.GetSaga().GetFailureCode() != saga.FailureCodeFraudRejected {
		t.Fatalf("saga = %+v, want the failed saga", resp.GetSaga())
	}
	if resp.GetSaga().GetFraudFlaggedAt() != nil {
		t.Fatalf("flagged_at = %v, want unset for a saga that was never flagged", resp.GetSaga().GetFraudFlaggedAt())
	}
	if len(resp.GetAudit()) != 1 {
		t.Fatalf("audit = %+v, want one record", resp.GetAudit())
	}
	record := resp.GetAudit()[0]
	if record.GetEventId() != "review.reject:payment-1" || record.GetPaymentId() != "payment-1" || record.GetKind() != saga.FraudAuditKindReviewRejected ||
		record.GetSagaState() != saga.StateFraudReview || record.GetDetailsJson() != `{"actor_id":"operator-1"}` || !record.GetCreatedAt().AsTime().Equal(fixedTime) {
		t.Fatalf("audit record = %+v", record)
	}
}

func TestGetFraudReviewRequiresPaymentID(t *testing.T) {
	server := NewServer(&fakeApp{})

	_, err := server.GetFraudReview(context.Background(), &sagav1.GetFraudReviewRequest{})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("code = %v, want %v", status.Code(err), codes.InvalidArgument)
	}
}

func TestGetFraudReviewMapsMissingSagaToNotFound(t *testing.T) {
	server := NewServer(&fakeApp{err: saga.ErrNotFound})

	_, err := server.GetFraudReview(context.Background(), &sagav1.GetFraudReviewRequest{PaymentId: "payment-1"})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("code = %v, want %v", status.Code(err), codes.NotFound)
	}
}

func (app *fakeApp) ListFraudReviews(context.Context) ([]saga.Saga, error) {
	if app.err != nil {
		return nil, app.err
	}
	return app.queue, nil
}

func (app *fakeApp) GetFraudReview(_ context.Context, paymentID string) (saga.FraudReview, error) {
	app.reviewPaymentID = paymentID
	if app.err != nil {
		return saga.FraudReview{}, app.err
	}
	return app.review, nil
}
