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
	"enjoythings/services/internal/verification"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestVerificationSubmitRouteUsesPrincipalAndReturnsStatus(t *testing.T) {
	userID := uuid.New()
	decidedAt := time.Date(2026, 6, 4, 10, 30, 0, 0, time.UTC)
	client := &fakeVerificationClient{
		submitRecord: verification.Record{
			VerificationID: "ver-1",
			UserID:         userID.String(),
			Status:         verification.StatusVerified,
			DecidedAt:      decidedAt,
		},
	}
	handler := auth.Middleware(testSecret)(NewVerification(client))
	req := authedRequest(t, http.MethodPost, "/v1/verification/submit", bytes.NewBufferString(`{"idempotency_key":"key-1","trace_id":"trace-1"}`), userID)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if client.submitCommand.UserID != userID.String() || client.submitCommand.IdempotencyKey != "key-1" || client.submitCommand.TraceID != "trace-1" {
		t.Fatalf("submit command = %+v", client.submitCommand)
	}
	var response struct {
		VerificationID string    `json:"verification_id"`
		UserID         string    `json:"user_id"`
		Status         string    `json:"status"`
		DecidedAt      time.Time `json:"decided_at"`
		TraceID        string    `json:"trace_id"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.VerificationID != "ver-1" || response.UserID != userID.String() || response.Status != verification.StatusVerified || !response.DecidedAt.Equal(decidedAt) || response.TraceID != "trace-1" {
		t.Fatalf("response = %+v", response)
	}
}

func TestVerificationStatusRouteMapsNotFound(t *testing.T) {
	userID := uuid.New()
	client := &fakeVerificationClient{statusErr: status.Error(codes.NotFound, "verification not found")}
	handler := auth.Middleware(testSecret)(NewVerification(client))
	req := authedRequest(t, http.MethodGet, "/v1/verification/status", nil, userID)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d; body %s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
}

type fakeVerificationClient struct {
	submitCommand verification.SubmitCommand
	submitRecord  verification.Record
	submitErr     error
	statusUserID  string
	statusRecord  verification.Record
	statusErr     error
}

func (client *fakeVerificationClient) Submit(ctx context.Context, cmd verification.SubmitCommand) (verification.Record, error) {
	client.submitCommand = cmd
	return client.submitRecord, client.submitErr
}

func (client *fakeVerificationClient) GetStatus(ctx context.Context, userID string) (verification.Record, error) {
	client.statusUserID = userID
	if client.statusRecord.UserID == "" {
		client.statusRecord.UserID = userID
	}
	return client.statusRecord, client.statusErr
}
