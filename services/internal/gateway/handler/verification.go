package handler

import (
	"context"
	"encoding/json"
	"net/http"

	"enjoythings/services/internal/auth"
	"enjoythings/services/internal/verification"
)

type VerificationClient interface {
	Submit(context.Context, verification.SubmitCommand) (verification.Record, error)
	GetStatus(context.Context, string) (verification.Record, error)
}

func NewVerification(client VerificationClient) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, ok := auth.PrincipalFromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized", "authentication required")
			return
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/verification/submit":
			handleSubmitVerification(w, r, client, principal.UserID.String())
		case r.Method == http.MethodGet && r.URL.Path == "/v1/verification/status":
			record, err := client.GetStatus(r.Context(), principal.UserID.String())
			if err != nil {
				writeGRPCError(w, err)
				return
			}
			writeJSON(w, http.StatusOK, verificationStatusResponse(record, r.URL.Query().Get("trace_id")))
		default:
			writeNotFound(w)
		}
	})
}

func handleSubmitVerification(w http.ResponseWriter, r *http.Request, client VerificationClient, userID string) {
	defer func() { _ = r.Body.Close() }()
	var request struct {
		PaymentID      string `json:"payment_id"`
		IdempotencyKey string `json:"idempotency_key"`
		TraceID        string `json:"trace_id"`
		VerificationID string `json:"verification_id"`
		Decision       string `json:"decision"`
		Reason         string `json:"reason"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "request body is invalid")
		return
	}
	record, err := client.Submit(r.Context(), verification.SubmitCommand{
		PaymentID:      request.PaymentID,
		IdempotencyKey: request.IdempotencyKey,
		TraceID:        request.TraceID,
		UserID:         userID,
		VerificationID: request.VerificationID,
		Decision:       request.Decision,
		Reason:         request.Reason,
	})
	if err != nil {
		writeGRPCError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, verificationSubmitResponse(record, request.TraceID))
}

func verificationSubmitResponse(record verification.Record, traceID string) map[string]any {
	return map[string]any{
		"verification_id": record.VerificationID,
		"user_id":         record.UserID,
		"status":          record.Status,
		"decided_at":      record.DecidedAt,
		"trace_id":        traceID,
	}
}

func verificationStatusResponse(record verification.Record, traceID string) map[string]any {
	return map[string]any{
		"user_id":         record.UserID,
		"status":          record.Status,
		"verification_id": record.VerificationID,
		"reason":          record.Reason,
		"updated_at":      record.UpdatedAt,
		"trace_id":        traceID,
	}
}
