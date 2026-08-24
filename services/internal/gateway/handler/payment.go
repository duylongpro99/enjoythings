package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"enjoythings/services/internal/auth"
	"enjoythings/services/internal/saga"

	"github.com/google/uuid"
)

type PaymentClient interface {
	StartPayment(context.Context, saga.StartRequest) (saga.Saga, error)
	GetPayment(context.Context, string, string) (saga.Saga, error)
}

func NewPayments(client PaymentClient) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, ok := auth.PrincipalFromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized", "authentication required")
			return
		}
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/transfers":
			startPayment(w, r, client, principal.UserID.String())
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/v1/payments/"):
			getPayment(w, r, client)
		default:
			writeNotFound(w)
		}
	})
}

func startPayment(w http.ResponseWriter, r *http.Request, client PaymentClient, userID string) {
	if rejectNonJSONContentType(w, r) {
		return
	}
	defer func() { _ = r.Body.Close() }()
	var request struct {
		PaymentID   string `json:"payment_id"`
		FromWallet  string `json:"from_wallet_id"`
		ToWallet    string `json:"to_wallet_id"`
		AmountCents int64  `json:"amount_cents"`
		Currency    string `json:"currency"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "request body is invalid")
		return
	}
	if request.PaymentID == "" {
		request.PaymentID = uuid.NewString()
	}
	if _, err := uuid.Parse(request.PaymentID); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "payment_id is invalid")
		return
	}
	if _, err := uuid.Parse(request.FromWallet); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "from_wallet_id is invalid")
		return
	}
	if _, err := uuid.Parse(request.ToWallet); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "to_wallet_id is invalid")
		return
	}
	if request.AmountCents <= 0 || request.Currency == "" || request.FromWallet == request.ToWallet {
		writeError(w, http.StatusBadRequest, "invalid_request", "payment fields are invalid")
		return
	}
	traceID := requestTraceID(r)
	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if idempotencyKey == "" {
		idempotencyKey = request.PaymentID
	}
	current, err := client.StartPayment(r.Context(), saga.StartRequest{
		PaymentID:      request.PaymentID,
		IdempotencyKey: idempotencyKey,
		TraceID:        traceID,
		UserID:         userID,
		FromWalletID:   request.FromWallet,
		ToWalletID:     request.ToWallet,
		AmountCents:    request.AmountCents,
		Currency:       strings.ToUpper(request.Currency),
	})
	if err != nil {
		writeGRPCError(w, err)
		return
	}
	w.Header().Set("X-Trace-Id", traceID)
	writeJSON(w, http.StatusAccepted, map[string]any{
		"payment_id": current.PaymentID,
		"status":     current.State,
		"trace_id":   traceID,
	})
}

func getPayment(w http.ResponseWriter, r *http.Request, client PaymentClient) {
	paymentID := strings.TrimPrefix(r.URL.Path, "/v1/payments/")
	if _, err := uuid.Parse(paymentID); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "payment_id is invalid")
		return
	}
	traceID := requestTraceID(r)
	current, err := client.GetPayment(r.Context(), paymentID, traceID)
	if err != nil {
		writeGRPCError(w, err)
		return
	}
	w.Header().Set("X-Trace-Id", traceID)
	writeJSON(w, http.StatusOK, map[string]any{
		"payment_id":      current.PaymentID,
		"status":          current.State,
		"from_wallet_id":  current.FromWalletID,
		"to_wallet_id":    current.ToWalletID,
		"amount_cents":    current.AmountCents,
		"currency":        current.Currency,
		"failure_code":    current.FailureCode,
		"failure_message": current.LastError,
		"created_at":      current.CreatedAt,
		"updated_at":      current.UpdatedAt,
		"trace_id":        traceID,
	})
}

func requestTraceID(r *http.Request) string {
	traceID := strings.TrimSpace(r.Header.Get("X-Trace-Id"))
	if traceID == "" {
		traceID = uuid.NewString()
	}
	return traceID
}
