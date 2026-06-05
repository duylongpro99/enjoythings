package paymentprocessor

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	StubTerminalFailureAmountCents int64 = 40901
	StubRetryOnceAmountCents       int64 = 50301
)

type StubRailHandler struct {
	mu           sync.Mutex
	callsByID    map[string]int
	timeoutSleep time.Duration
}

func NewStubRailHandler(timeoutSleep time.Duration) *StubRailHandler {
	if timeoutSleep <= 0 {
		timeoutSleep = 3 * time.Second
	}
	return &StubRailHandler{callsByID: make(map[string]int), timeoutSleep: timeoutSleep}
}

func (handler *StubRailHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet && (r.URL.Path == "/healthz" || r.URL.Path == "/readyz") {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
		return
	}

	if r.Method != http.MethodPost || r.URL.Path != "/charge" {
		http.NotFound(w, r)
		return
	}
	var req RailChargeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.PaymentID == "" || req.AmountCents <= 0 || req.Currency == "" {
		writeRailFailure(w, http.StatusBadRequest, "invalid_payment", "invalid payment request")
		return
	}

	handler.mu.Lock()
	handler.callsByID[req.PaymentID]++
	callCount := handler.callsByID[req.PaymentID]
	handler.mu.Unlock()

	switch {
	case strings.Contains(req.PaymentID, "terminal") || req.AmountCents == StubTerminalFailureAmountCents:
		writeRailFailure(w, http.StatusBadRequest, "terminal_failure", "terminal rail failure")
		return
	case (strings.Contains(req.PaymentID, "retry") || req.AmountCents == StubRetryOnceAmountCents) && callCount == 1:
		writeRailFailure(w, http.StatusServiceUnavailable, "rail_unavailable", "retryable rail failure")
		return
	case strings.Contains(req.PaymentID, "timeout"):
		time.Sleep(handler.timeoutSleep)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		ProcessorPaymentID string    `json:"processor_payment_id"`
		CompletedAt        time.Time `json:"completed_at"`
	}{
		ProcessorPaymentID: "stub-" + req.PaymentID,
		CompletedAt:        time.Now().UTC(),
	})
}

func writeRailFailure(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}{
		Code:    code,
		Message: message,
	})
}
