package handler

import (
	"bytes"
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

func TestPaymentTransferStartsSagaAndReturnsAccepted(t *testing.T) {
	userID := uuid.New()
	fromID := uuid.New()
	toID := uuid.New()
	client := &fakePaymentClient{started: saga.Saga{
		PaymentID: "11111111-1111-1111-1111-111111111111",
		State:     saga.StatePaymentProcessing,
	}}
	handler := auth.Middleware(testSecret)(NewPayments(client))
	body := `{"payment_id":"11111111-1111-1111-1111-111111111111","from_wallet_id":"` + fromID.String() + `","to_wallet_id":"` + toID.String() + `","amount_cents":1250,"currency":"USD"}`
	req := authedRequest(t, http.MethodPost, "/v1/transfers", bytes.NewBufferString(body), userID)
	req.Header.Set("Idempotency-Key", "idem-1")
	req.Header.Set("X-Trace-Id", "trace-1")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want %d; body %s", rec.Code, http.StatusAccepted, rec.Body.String())
	}
	if client.start.UserID != userID.String() || client.start.IdempotencyKey != "idem-1" || client.start.TraceID != "trace-1" || client.start.AmountCents != 1250 {
		t.Fatalf("start request = %+v", client.start)
	}
	if rec.Header().Get("X-Trace-Id") != "trace-1" {
		t.Fatalf("response trace id = %q, want trace-1", rec.Header().Get("X-Trace-Id"))
	}
	var response struct {
		PaymentID string `json:"payment_id"`
		Status    string `json:"status"`
		TraceID   string `json:"trace_id"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.PaymentID != client.started.PaymentID || response.Status != saga.StatePaymentProcessing || response.TraceID != "trace-1" {
		t.Fatalf("response = %+v", response)
	}
}

func TestPaymentTransferMapsUnverifiedSagaTo422(t *testing.T) {
	userID := uuid.New()
	client := &fakePaymentClient{err: status.Error(codes.FailedPrecondition, "user is not verified")}
	handler := auth.Middleware(testSecret)(NewPayments(client))
	body := `{"from_wallet_id":"` + uuid.NewString() + `","to_wallet_id":"` + uuid.NewString() + `","amount_cents":1250,"currency":"USD"}`
	req := authedRequest(t, http.MethodPost, "/v1/transfers", bytes.NewBufferString(body), userID)
	req.Header.Set("Idempotency-Key", "idem-1")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d; body %s", rec.Code, http.StatusUnprocessableEntity, rec.Body.String())
	}
}

func TestPaymentStatusReturnsSaga(t *testing.T) {
	client := &fakePaymentClient{got: saga.Saga{
		PaymentID:    "11111111-1111-1111-1111-111111111111",
		State:        saga.StateCompleted,
		FromWalletID: uuid.NewString(),
		ToWalletID:   uuid.NewString(),
		AmountCents:  1250,
		Currency:     "USD",
		CreatedAt:    time.Date(2026, 6, 5, 10, 0, 0, 0, time.UTC),
		UpdatedAt:    time.Date(2026, 6, 5, 10, 1, 0, 0, time.UTC),
	}}
	handler := auth.Middleware(testSecret)(NewPayments(client))
	req := authedRequest(t, http.MethodGet, "/v1/payments/"+client.got.PaymentID, nil, uuid.New())
	req.Header.Set("X-Trace-Id", "trace-status")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if client.gotPaymentID != client.got.PaymentID || client.gotTraceID != "trace-status" {
		t.Fatalf("get args = %q/%q", client.gotPaymentID, client.gotTraceID)
	}
}

type fakePaymentClient struct {
	start        saga.StartRequest
	started      saga.Saga
	gotPaymentID string
	gotTraceID   string
	got          saga.Saga
	err          error
	decision     saga.FraudReviewDecision
	decided      string
	reviewed     saga.Saga
	reviewErr    error

	queue           []saga.Saga
	listTraceID     string
	review          saga.FraudReview
	reviewPaymentID string
	reviewTraceID   string
}

func (client *fakePaymentClient) ResumeFraudReview(_ context.Context, decision saga.FraudReviewDecision) (saga.Saga, error) {
	client.decision = decision
	client.decided = "resume"
	return client.reviewed, client.reviewErr
}

func (client *fakePaymentClient) RejectFraudReview(_ context.Context, decision saga.FraudReviewDecision) (saga.Saga, error) {
	client.decision = decision
	client.decided = "reject"
	return client.reviewed, client.reviewErr
}

func (client *fakePaymentClient) StartPayment(_ context.Context, req saga.StartRequest) (saga.Saga, error) {
	client.start = req
	return client.started, client.err
}

func (client *fakePaymentClient) GetPayment(_ context.Context, paymentID, traceID string) (saga.Saga, error) {
	client.gotPaymentID = paymentID
	client.gotTraceID = traceID
	return client.got, client.err
}
