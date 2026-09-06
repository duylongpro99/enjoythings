package client

import (
	"context"
	"testing"
	"time"

	sagav1 "enjoythings/services/gen/saga/v1"
	"enjoythings/services/internal/saga"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestSagaClientMapsFraudReviewQueueAndDetail(t *testing.T) {
	now := time.Date(2026, 6, 5, 10, 0, 0, 0, time.UTC)
	held := &sagav1.PaymentSaga{
		PaymentId:           "payment-1",
		Status:              saga.StateFraudReview,
		UserId:              "user-1",
		FromWalletId:        "from-1",
		ToWalletId:          "to-1",
		AmountCents:         1250,
		Currency:            "USD",
		FraudSessionId:      "session-1",
		FraudAction:         "block",
		FraudRiskScore:      0.95,
		FraudReason:         "high velocity",
		FraudFlaggedAt:      timestamppb.New(now),
		DeferredPaymentJson: `{"event_id":"payment.completed:payment-1"}`,
		CreatedAt:           timestamppb.New(now.Add(-time.Minute)),
		UpdatedAt:           timestamppb.New(now),
	}
	grpcClient := &fakeSagaServiceClient{
		listResponse: &sagav1.ListFraudReviewsResponse{Sagas: []*sagav1.PaymentSaga{held}},
		detailResponse: &sagav1.GetFraudReviewResponse{
			Saga: held,
			Audit: []*sagav1.FraudAuditRecord{{
				EventId:     "fraud.flagged:1",
				PaymentId:   "payment-1",
				Kind:        saga.FraudAuditKindTransition,
				SagaState:   saga.StatePaymentProcessing,
				DetailsJson: `{"risk_score":0.95}`,
				CreatedAt:   timestamppb.New(now),
			}},
		},
	}
	client := NewSagaClient(grpcClient)

	queue, err := client.ListFraudReviews(context.Background(), "trace-1")
	if err != nil {
		t.Fatalf("ListFraudReviews: %v", err)
	}
	if grpcClient.listRequest.GetTraceId() != "trace-1" {
		t.Fatalf("list request = %+v, want trace-1", grpcClient.listRequest)
	}
	if len(queue) != 1 {
		t.Fatalf("queue = %+v, want one saga", queue)
	}
	got := queue[0]
	if got.PaymentID != "payment-1" || got.State != saga.StateFraudReview || got.UserID != "user-1" ||
		got.FromWalletID != "from-1" || got.AmountCents != 1250 || got.Currency != "USD" {
		t.Fatalf("saga = %+v, want the held payment", got)
	}
	if got.FraudSessionID != "session-1" || got.FraudAction != "block" || got.FraudRiskScore != 0.95 ||
		got.FraudReason != "high velocity" || !got.FraudFlaggedAt.Equal(now) || got.DeferredPaymentJSON != held.GetDeferredPaymentJson() {
		t.Fatalf("saga verdict = %+v, want the fraud fields mapped", got)
	}

	review, err := client.GetFraudReview(context.Background(), "payment-1", "trace-2")
	if err != nil {
		t.Fatalf("GetFraudReview: %v", err)
	}
	if grpcClient.reviewRequest.GetPaymentId() != "payment-1" || grpcClient.reviewRequest.GetTraceId() != "trace-2" {
		t.Fatalf("detail request = %+v", grpcClient.reviewRequest)
	}
	if review.Saga.PaymentID != "payment-1" || len(review.Audit) != 1 {
		t.Fatalf("review = %+v, want the saga with one audit record", review)
	}
	audit := review.Audit[0]
	if audit.EventID != "fraud.flagged:1" || audit.PaymentID != "payment-1" || audit.Kind != saga.FraudAuditKindTransition ||
		audit.SagaState != saga.StatePaymentProcessing || audit.DetailsJSON != `{"risk_score":0.95}` || !audit.CreatedAt.Equal(now) {
		t.Fatalf("audit = %+v", audit)
	}
}

func TestSagaClientLeavesUnflaggedTimeZero(t *testing.T) {
	grpcClient := &fakeSagaServiceClient{
		listResponse: &sagav1.ListFraudReviewsResponse{Sagas: []*sagav1.PaymentSaga{{PaymentId: "payment-1"}}},
	}

	queue, err := NewSagaClient(grpcClient).ListFraudReviews(context.Background(), "")
	if err != nil {
		t.Fatalf("ListFraudReviews: %v", err)
	}
	if len(queue) != 1 || !queue[0].FraudFlaggedAt.IsZero() {
		t.Fatalf("queue = %+v, want one saga with a zero flagged-at", queue)
	}
}

func (client *fakeSagaServiceClient) ListFraudReviews(_ context.Context, req *sagav1.ListFraudReviewsRequest, _ ...grpc.CallOption) (*sagav1.ListFraudReviewsResponse, error) {
	client.listRequest = req
	return client.listResponse, client.err
}

func (client *fakeSagaServiceClient) GetFraudReview(_ context.Context, req *sagav1.GetFraudReviewRequest, _ ...grpc.CallOption) (*sagav1.GetFraudReviewResponse, error) {
	client.reviewRequest = req
	return client.detailResponse, client.err
}
