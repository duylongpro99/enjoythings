package handler

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"enjoythings/services/internal/auth"
	"enjoythings/services/internal/saga"

	"github.com/google/uuid"
)

const fraudReviewsPath = "/v1/fraud-reviews"

// NewFraudReviews serves the operator review queue: the sagas held in
// FRAUD_REVIEW and, per payment, the saga with its fraud audit trail. Reading
// the queue exposes other users' payments, so every route here needs the same
// admin role as the decisions under /v1/payments/{id}/fraud-review.
func NewFraudReviews(client PaymentClient) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, ok := auth.PrincipalFromContext(r.Context())
		if !ok {
			writeError(w, http.StatusUnauthorized, "unauthorized", "authentication required")
			return
		}
		if principal.Role != RoleAdmin {
			writeError(w, http.StatusForbidden, "forbidden", "the fraud review queue requires an administrator")
			return
		}
		paymentID, hasPaymentID := strings.CutPrefix(r.URL.Path, fraudReviewsPath+"/")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == fraudReviewsPath:
			listFraudReviews(w, r, client)
		case r.Method == http.MethodGet && hasPaymentID && !strings.Contains(paymentID, "/"):
			getFraudReview(w, r, client, paymentID)
		default:
			writeNotFound(w)
		}
	})
}

func listFraudReviews(w http.ResponseWriter, r *http.Request, client PaymentClient) {
	traceID := requestTraceID(r)
	sagas, err := client.ListFraudReviews(r.Context(), traceID)
	if err != nil {
		writeGRPCError(w, err)
		return
	}
	reviews := make([]map[string]any, 0, len(sagas))
	for _, current := range sagas {
		reviews = append(reviews, fraudReviewResponse(current))
	}
	w.Header().Set("X-Trace-Id", traceID)
	writeJSON(w, http.StatusOK, map[string]any{
		"reviews":  reviews,
		"trace_id": traceID,
	})
}

func getFraudReview(w http.ResponseWriter, r *http.Request, client PaymentClient, paymentID string) {
	if _, err := uuid.Parse(paymentID); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "payment_id is invalid")
		return
	}
	traceID := requestTraceID(r)
	review, err := client.GetFraudReview(r.Context(), paymentID, traceID)
	if err != nil {
		writeGRPCError(w, err)
		return
	}
	audit := make([]map[string]any, 0, len(review.Audit))
	for _, record := range review.Audit {
		audit = append(audit, fraudAuditResponse(record))
	}
	w.Header().Set("X-Trace-Id", traceID)
	writeJSON(w, http.StatusOK, map[string]any{
		"review":   fraudReviewResponse(review.Saga),
		"audit":    audit,
		"trace_id": traceID,
	})
}

func fraudReviewResponse(current saga.Saga) map[string]any {
	return map[string]any{
		"payment_id":              current.PaymentID,
		"status":                  current.State,
		"user_id":                 current.UserID,
		"from_wallet_id":          current.FromWalletID,
		"to_wallet_id":            current.ToWalletID,
		"amount_cents":            current.AmountCents,
		"currency":                current.Currency,
		"fraud_session_id":        current.FraudSessionID,
		"fraud_action":            current.FraudAction,
		"fraud_risk_score":        current.FraudRiskScore,
		"fraud_reason":            current.FraudReason,
		"fraud_flagged_at":        optionalTime(current.FraudFlaggedAt),
		"deferred_result_pending": current.DeferredPaymentJSON != "",
		"failure_code":            current.FailureCode,
		"failure_message":         current.LastError,
		"created_at":              current.CreatedAt,
		"updated_at":              current.UpdatedAt,
	}
}

// fraudAuditResponse embeds the stored details as JSON when they are JSON, so
// a client reads structured fields instead of re-parsing a string.
func fraudAuditResponse(record saga.FraudAuditRecord) map[string]any {
	var details any = record.DetailsJSON
	if json.Valid([]byte(record.DetailsJSON)) {
		details = json.RawMessage(record.DetailsJSON)
	}
	return map[string]any{
		"event_id":   record.EventID,
		"kind":       record.Kind,
		"saga_state": record.SagaState,
		"details":    details,
		"created_at": record.CreatedAt,
	}
}

// optionalTime renders an unset time as null rather than year one.
func optionalTime(value time.Time) any {
	if value.IsZero() {
		return nil
	}
	return value
}
