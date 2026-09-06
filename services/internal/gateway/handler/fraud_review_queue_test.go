package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"enjoythings/services/internal/auth"
	"enjoythings/services/internal/saga"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestFraudReviewQueueRequiresAdminRole(t *testing.T) {
	client := &fakePaymentClient{}
	handler := auth.Middleware(testSecret)(NewFraudReviews(client))
	req := roleRequest(t, http.MethodGet, fraudReviewsPath, nil, uuid.New(), "user")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body %s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
	if client.listTraceID != "" {
		t.Fatalf("client was called with trace %q, want no call", client.listTraceID)
	}
}

func TestFraudReviewQueueListsHeldPayments(t *testing.T) {
	flaggedAt := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	held := saga.Saga{
		PaymentID:           uuid.NewString(),
		State:               saga.StateFraudReview,
		UserID:              uuid.NewString(),
		FromWalletID:        uuid.NewString(),
		ToWalletID:          uuid.NewString(),
		AmountCents:         1250,
		Currency:            "USD",
		FraudSessionID:      "session-1",
		FraudAction:         "block",
		FraudRiskScore:      0.95,
		FraudReason:         "high velocity",
		FraudFlaggedAt:      flaggedAt,
		DeferredPaymentJSON: `{"event_id":"payment.completed:x"}`,
		CreatedAt:           flaggedAt.Add(-time.Minute),
		UpdatedAt:           flaggedAt,
	}
	client := &fakePaymentClient{queue: []saga.Saga{held}}
	handler := auth.Middleware(testSecret)(NewFraudReviews(client))
	req := roleRequest(t, http.MethodGet, fraudReviewsPath, nil, uuid.New(), RoleAdmin)
	req.Header.Set("X-Trace-Id", "trace-queue")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if client.listTraceID != "trace-queue" {
		t.Fatalf("list trace = %q, want the request trace", client.listTraceID)
	}
	var response struct {
		Reviews []struct {
			PaymentID             string    `json:"payment_id"`
			Status                string    `json:"status"`
			UserID                string    `json:"user_id"`
			AmountCents           int64     `json:"amount_cents"`
			Currency              string    `json:"currency"`
			FraudSessionID        string    `json:"fraud_session_id"`
			FraudAction           string    `json:"fraud_action"`
			FraudRiskScore        float64   `json:"fraud_risk_score"`
			FraudReason           string    `json:"fraud_reason"`
			FraudFlaggedAt        time.Time `json:"fraud_flagged_at"`
			DeferredResultPending bool      `json:"deferred_result_pending"`
		} `json:"reviews"`
		TraceID string `json:"trace_id"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(response.Reviews) != 1 || response.TraceID != "trace-queue" {
		t.Fatalf("response = %+v, want one review with the request trace", response)
	}
	got := response.Reviews[0]
	if got.PaymentID != held.PaymentID || got.Status != saga.StateFraudReview || got.UserID != held.UserID ||
		got.AmountCents != 1250 || got.Currency != "USD" {
		t.Fatalf("review = %+v, want the held payment", got)
	}
	if got.FraudSessionID != "session-1" || got.FraudAction != "block" || got.FraudRiskScore != 0.95 ||
		got.FraudReason != "high velocity" || !got.FraudFlaggedAt.Equal(flaggedAt) {
		t.Fatalf("review verdict = %+v, want the fraud worker's verdict", got)
	}
	if !got.DeferredResultPending {
		t.Fatal("deferred_result_pending = false, want true for a saga holding a rail result")
	}
}

func TestFraudReviewDetailReturnsSagaAndAuditTrail(t *testing.T) {
	paymentID := uuid.New()
	createdAt := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	client := &fakePaymentClient{review: saga.FraudReview{
		Saga: saga.Saga{PaymentID: paymentID.String(), State: saga.StateFraudReview, FraudRiskScore: 0.95},
		Audit: []saga.FraudAuditRecord{
			{
				EventID:     "fraud.flagged:1",
				PaymentID:   paymentID.String(),
				Kind:        saga.FraudAuditKindTransition,
				SagaState:   saga.StatePaymentProcessing,
				DetailsJSON: `{"risk_score":0.95,"reason":"high velocity"}`,
				CreatedAt:   createdAt,
			},
			{EventID: "legacy", PaymentID: paymentID.String(), Kind: "note", DetailsJSON: "not json", CreatedAt: createdAt},
		},
	}}
	handler := auth.Middleware(testSecret)(NewFraudReviews(client))
	req := roleRequest(t, http.MethodGet, fraudReviewsPath+"/"+paymentID.String(), nil, uuid.New(), RoleAdmin)
	req.Header.Set("X-Trace-Id", "trace-detail")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if client.reviewPaymentID != paymentID.String() || client.reviewTraceID != "trace-detail" {
		t.Fatalf("detail args = %q/%q, want the payment and request trace", client.reviewPaymentID, client.reviewTraceID)
	}
	var response struct {
		Review struct {
			PaymentID             string     `json:"payment_id"`
			Status                string     `json:"status"`
			FraudFlaggedAt        *time.Time `json:"fraud_flagged_at"`
			DeferredResultPending bool       `json:"deferred_result_pending"`
		} `json:"review"`
		Audit []struct {
			EventID   string          `json:"event_id"`
			Kind      string          `json:"kind"`
			SagaState string          `json:"saga_state"`
			Details   json.RawMessage `json:"details"`
		} `json:"audit"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Review.PaymentID != paymentID.String() || response.Review.Status != saga.StateFraudReview {
		t.Fatalf("review = %+v, want the held saga", response.Review)
	}
	if response.Review.FraudFlaggedAt != nil || response.Review.DeferredResultPending {
		t.Fatalf("review = %+v, want null flagged-at and no deferred result", response.Review)
	}
	if len(response.Audit) != 2 || response.Audit[0].Kind != saga.FraudAuditKindTransition || response.Audit[0].SagaState != saga.StatePaymentProcessing {
		t.Fatalf("audit = %+v, want the transition first", response.Audit)
	}
	var details struct {
		RiskScore float64 `json:"risk_score"`
	}
	if err := json.Unmarshal(response.Audit[0].Details, &details); err != nil || details.RiskScore != 0.95 {
		t.Fatalf("details = %s (%v), want embedded JSON with the risk score", response.Audit[0].Details, err)
	}
	if string(response.Audit[1].Details) != `"not json"` {
		t.Fatalf("non-JSON details = %s, want the raw string", response.Audit[1].Details)
	}
}

func TestFraudReviewDetailRejectsInvalidPaymentID(t *testing.T) {
	client := &fakePaymentClient{}
	handler := auth.Middleware(testSecret)(NewFraudReviews(client))
	req := roleRequest(t, http.MethodGet, fraudReviewsPath+"/not-a-uuid", nil, uuid.New(), RoleAdmin)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if client.reviewPaymentID != "" {
		t.Fatalf("client was called with %q, want no call", client.reviewPaymentID)
	}
}

func TestFraudReviewDetailMapsMissingPaymentToNotFound(t *testing.T) {
	client := &fakePaymentClient{err: status.Error(codes.NotFound, "payment saga not found")}
	handler := auth.Middleware(testSecret)(NewFraudReviews(client))
	req := roleRequest(t, http.MethodGet, fraudReviewsPath+"/"+uuid.NewString(), nil, uuid.New(), RoleAdmin)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body %s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
}

func TestFraudReviewRoutesRejectOtherShapes(t *testing.T) {
	client := &fakePaymentClient{}
	handler := auth.Middleware(testSecret)(NewFraudReviews(client))
	for _, target := range []struct{ method, path string }{
		{http.MethodPost, fraudReviewsPath},
		{http.MethodGet, fraudReviewsPath + "/" + uuid.NewString() + "/audit"},
	} {
		req := roleRequest(t, target.method, target.path, nil, uuid.New(), RoleAdmin)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s %s status = %d, want %d", target.method, target.path, rec.Code, http.StatusNotFound)
		}
	}
	if client.listTraceID != "" || client.reviewPaymentID != "" {
		t.Fatal("client was called for an unrouted request")
	}
}

func (client *fakePaymentClient) ListFraudReviews(_ context.Context, traceID string) ([]saga.Saga, error) {
	client.listTraceID = traceID
	return client.queue, client.err
}

func (client *fakePaymentClient) GetFraudReview(_ context.Context, paymentID, traceID string) (saga.FraudReview, error) {
	client.reviewPaymentID = paymentID
	client.reviewTraceID = traceID
	return client.review, client.err
}
