package paymentprocessor

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHTTPRailChargeReturnsSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/charge" {
			t.Fatalf("request = %s %s, want POST /charge", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"processor_payment_id":"rail-payment-1","completed_at":"2026-06-04T01:02:03Z"}`))
	}))
	t.Cleanup(server.Close)

	rail := NewHTTPRail(server.URL, server.Client())
	result, err := rail.Charge(context.Background(), RailChargeRequest{
		PaymentID:      "payment-1",
		IdempotencyKey: "payment-1:execute-payment",
		AmountCents:    1250,
		Currency:       "USD",
	})
	if err != nil {
		t.Fatalf("Charge: %v", err)
	}
	if result.ProcessorPaymentID != "rail-payment-1" {
		t.Fatalf("ProcessorPaymentID = %q, want rail-payment-1", result.ProcessorPaymentID)
	}
}

func TestHTTPRailClassifiesServerErrorAsRetryable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "try later", http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)

	rail := NewHTTPRail(server.URL, server.Client())
	_, err := rail.Charge(context.Background(), RailChargeRequest{PaymentID: "payment-1", IdempotencyKey: "idem-1", AmountCents: 1250, Currency: "USD"})
	if err == nil {
		t.Fatal("expected rail error")
	}
	var railErr RailError
	if !errors.As(err, &railErr) || !railErr.Failure.Retryable {
		t.Fatalf("error = %v, want retryable RailError", err)
	}
}

func TestHTTPRailClassifiesClientErrorAsTerminal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"code":"invalid_payment","message":"invalid payment"}`))
	}))
	t.Cleanup(server.Close)

	rail := NewHTTPRail(server.URL, server.Client())
	_, err := rail.Charge(context.Background(), RailChargeRequest{PaymentID: "payment-1", IdempotencyKey: "idem-1", AmountCents: 1250, Currency: "USD"})
	if err == nil {
		t.Fatal("expected rail error")
	}
	var railErr RailError
	if !errors.As(err, &railErr) || railErr.Failure.Retryable || railErr.Failure.Code != "invalid_payment" {
		t.Fatalf("error = %v, want terminal invalid_payment RailError", err)
	}
}

func TestHTTPRailClassifiesTimeoutAsRetryable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
	}))
	t.Cleanup(server.Close)

	rail := NewHTTPRail(server.URL, &http.Client{Timeout: time.Millisecond})
	_, err := rail.Charge(context.Background(), RailChargeRequest{PaymentID: "payment-1", IdempotencyKey: "idem-1", AmountCents: 1250, Currency: "USD"})
	if err == nil {
		t.Fatal("expected timeout error")
	}
	var railErr RailError
	if !errors.As(err, &railErr) || !railErr.Failure.Retryable {
		t.Fatalf("error = %v, want retryable RailError", err)
	}
}
