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
	ResumeFraudReview(context.Context, saga.FraudReviewDecision) (saga.Saga, error)
	RejectFraudReview(context.Context, saga.FraudReviewDecision) (saga.Saga, error)
}

// RoleAdmin may resolve a fraud review. Ordinary callers cannot: releasing a
// held payment or refunding it is an operator action, not the payer's.
const RoleAdmin = "admin"

const paymentPathPrefix = "/v1/payments/"

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
		case r.Method == http.MethodPost && isFraudReviewPath(r.URL.Path):
			decideFraudReview(w, r, client, principal)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, paymentPathPrefix):
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
	paymentID := strings.TrimPrefix(r.URL.Path, paymentPathPrefix)
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

func isFraudReviewPath(path string) bool {
	_, action, ok := fraudReviewTarget(path)
	return ok && (action == "resume" || action == "reject")
}

// fraudReviewTarget splits the payment ID and action out of a review path.
func fraudReviewTarget(path string) (string, string, bool) {
	rest, ok := strings.CutPrefix(path, paymentPathPrefix)
	if !ok {
		return "", "", false
	}
	paymentID, tail, ok := strings.Cut(rest, "/")
	if !ok {
		return "", "", false
	}
	action, ok := strings.CutPrefix(tail, "fraud-review/")
	if !ok || action == "" || strings.Contains(action, "/") {
		return "", "", false
	}
	return paymentID, action, true
}

func decideFraudReview(w http.ResponseWriter, r *http.Request, client PaymentClient, principal auth.Principal) {
	if principal.Role != RoleAdmin {
		writeError(w, http.StatusForbidden, "forbidden", "fraud review decisions require an administrator")
		return
	}
	paymentID, action, ok := fraudReviewTarget(r.URL.Path)
	if !ok {
		writeNotFound(w)
		return
	}
	if _, err := uuid.Parse(paymentID); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "payment_id is invalid")
		return
	}

	// The reason is optional: a resume often needs no explanation, and a
	// rejection falls back to a fixed reason when none is given.
	reason := ""
	if r.ContentLength > 0 {
		if rejectNonJSONContentType(w, r) {
			return
		}
		defer func() { _ = r.Body.Close() }()
		var request struct {
			Reason string `json:"reason"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "request body is invalid")
			return
		}
		reason = strings.TrimSpace(request.Reason)
	}

	traceID := requestTraceID(r)
	decision := saga.FraudReviewDecision{
		PaymentID: paymentID,
		ActorID:   principal.UserID.String(),
		Reason:    reason,
		TraceID:   traceID,
	}

	var (
		current saga.Saga
		err     error
	)
	if action == "resume" {
		current, err = client.ResumeFraudReview(r.Context(), decision)
	} else {
		current, err = client.RejectFraudReview(r.Context(), decision)
	}
	if err != nil {
		writeGRPCError(w, err)
		return
	}

	w.Header().Set("X-Trace-Id", traceID)
	writeJSON(w, http.StatusOK, map[string]any{
		"payment_id":      current.PaymentID,
		"status":          current.State,
		"failure_code":    current.FailureCode,
		"failure_message": current.LastError,
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
