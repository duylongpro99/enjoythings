package handler

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"enjoythings/services/internal/auth"
	"enjoythings/services/internal/saga"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestFraudReviewResumeRequiresAdminRole(t *testing.T) {
	paymentID := uuid.New()
	client := &fakePaymentClient{}
	handler := auth.Middleware(testSecret)(NewPayments(client))
	req := roleRequest(t, http.MethodPost, reviewPath(paymentID, "resume"), nil, uuid.New(), "user")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d; body %s", rec.Code, http.StatusForbidden, rec.Body.String())
	}
	if client.decided != "" {
		t.Fatalf("client was called with %q, want no call", client.decided)
	}
}

func TestFraudReviewResumeSendsDecisionWithActorAndReason(t *testing.T) {
	paymentID := uuid.New()
	adminID := uuid.New()
	client := &fakePaymentClient{reviewed: saga.Saga{
		PaymentID: paymentID.String(),
		State:     saga.StateCompleted,
		UpdatedAt: time.Now().UTC(),
	}}
	handler := auth.Middleware(testSecret)(NewPayments(client))
	body := bytes.NewBufferString(`{"reason":"manual check cleared"}`)
	req := roleRequest(t, http.MethodPost, reviewPath(paymentID, "resume"), body, adminID, RoleAdmin)
	req.Header.Set("X-Trace-Id", "trace-review")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if client.decided != "resume" {
		t.Fatalf("decision = %q, want resume", client.decided)
	}
	if client.decision.PaymentID != paymentID.String() || client.decision.ActorID != adminID.String() {
		t.Fatalf("decision = %+v, want payment %s by admin %s", client.decision, paymentID, adminID)
	}
	if client.decision.Reason != "manual check cleared" || client.decision.TraceID != "trace-review" {
		t.Fatalf("decision = %+v, want the request reason and trace", client.decision)
	}

	var response struct {
		PaymentID string `json:"payment_id"`
		Status    string `json:"status"`
		TraceID   string `json:"trace_id"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.PaymentID != paymentID.String() || response.Status != saga.StateCompleted || response.TraceID != "trace-review" {
		t.Fatalf("response = %+v", response)
	}
}

func TestFraudReviewRejectWorksWithoutBody(t *testing.T) {
	paymentID := uuid.New()
	client := &fakePaymentClient{reviewed: saga.Saga{
		PaymentID:   paymentID.String(),
		State:       saga.StateFailed,
		FailureCode: saga.FailureCodeFraudRejected,
		LastError:   "rejected in fraud review",
	}}
	handler := auth.Middleware(testSecret)(NewPayments(client))
	req := roleRequest(t, http.MethodPost, reviewPath(paymentID, "reject"), nil, uuid.New(), RoleAdmin)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if client.decided != "reject" || client.decision.Reason != "" {
		t.Fatalf("decision = %q %+v, want a reject with no reason", client.decided, client.decision)
	}
	var response struct {
		FailureCode string `json:"failure_code"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.FailureCode != saga.FailureCodeFraudRejected {
		t.Fatalf("failure code = %q, want %q", response.FailureCode, saga.FailureCodeFraudRejected)
	}
}

func TestFraudReviewRejectsInvalidPaymentID(t *testing.T) {
	client := &fakePaymentClient{}
	handler := auth.Middleware(testSecret)(NewPayments(client))
	req := roleRequest(t, http.MethodPost, "/v1/payments/not-a-uuid/fraud-review/resume", nil, uuid.New(), RoleAdmin)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if client.decided != "" {
		t.Fatalf("client was called with %q, want no call", client.decided)
	}
}

func TestFraudReviewMapsNotUnderReviewTo422(t *testing.T) {
	paymentID := uuid.New()
	client := &fakePaymentClient{reviewErr: status.Error(codes.FailedPrecondition, "payment is not under fraud review")}
	handler := auth.Middleware(testSecret)(NewPayments(client))
	req := roleRequest(t, http.MethodPost, reviewPath(paymentID, "resume"), nil, uuid.New(), RoleAdmin)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d; body %s", rec.Code, http.StatusUnprocessableEntity, rec.Body.String())
	}
}

func TestUnknownFraudReviewActionIsNotFound(t *testing.T) {
	paymentID := uuid.New()
	client := &fakePaymentClient{}
	handler := auth.Middleware(testSecret)(NewPayments(client))
	req := roleRequest(t, http.MethodPost, reviewPath(paymentID, "approve"), nil, uuid.New(), RoleAdmin)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	if client.decided != "" {
		t.Fatalf("client was called with %q, want no call", client.decided)
	}
}

func reviewPath(paymentID uuid.UUID, action string) string {
	return "/v1/payments/" + paymentID.String() + "/fraud-review/" + action
}

func roleRequest(t *testing.T, method, target string, body io.Reader, userID uuid.UUID, role string) *http.Request {
	t.Helper()

	req := httptest.NewRequest(method, target, body)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"user_id": userID.String(),
		"role":    role,
		"exp":     time.Now().Add(time.Hour).Unix(),
	})
	signed, err := token.SignedString([]byte(testSecret))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+signed)
	return req
}
