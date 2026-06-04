package paymentprocessor

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestStubRailHandlerSucceedsByDefault(t *testing.T) {
	handler := NewStubRailHandler(time.Millisecond)
	req := httptest.NewRequest(http.MethodPost, "/charge", strings.NewReader(`{"payment_id":"payment-success","idempotency_key":"idem-1","amount_cents":1250,"currency":"USD"}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"processor_payment_id"`) {
		t.Fatalf("body = %s, want processor payment id", rec.Body.String())
	}
}

func TestStubRailHandlerFailsRetryableOnceThenSucceeds(t *testing.T) {
	handler := NewStubRailHandler(time.Millisecond)
	body := `{"payment_id":"payment-retry-once","idempotency_key":"idem-1","amount_cents":1250,"currency":"USD"}`

	first := httptest.NewRecorder()
	handler.ServeHTTP(first, httptest.NewRequest(http.MethodPost, "/charge", strings.NewReader(body)))
	if first.Code != http.StatusServiceUnavailable {
		t.Fatalf("first status = %d, want 503", first.Code)
	}

	second := httptest.NewRecorder()
	handler.ServeHTTP(second, httptest.NewRequest(http.MethodPost, "/charge", strings.NewReader(body)))
	if second.Code != http.StatusOK {
		t.Fatalf("second status = %d, want 200", second.Code)
	}
}

func TestStubRailHandlerReturnsTerminalFailure(t *testing.T) {
	handler := NewStubRailHandler(time.Millisecond)
	req := httptest.NewRequest(http.MethodPost, "/charge", strings.NewReader(`{"payment_id":"payment-terminal-failure","idempotency_key":"idem-1","amount_cents":1250,"currency":"USD"}`))
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "terminal_failure") {
		t.Fatalf("body = %s, want terminal_failure", rec.Body.String())
	}
}

func TestStubRailHandlerTimeoutScenarioSleepsThenSucceeds(t *testing.T) {
	handler := NewStubRailHandler(time.Millisecond)
	req := httptest.NewRequest(http.MethodPost, "/charge", strings.NewReader(`{"payment_id":"payment-timeout","idempotency_key":"idem-1","amount_cents":1250,"currency":"USD"}`))
	rec := httptest.NewRecorder()

	started := time.Now()
	handler.ServeHTTP(rec, req)

	if time.Since(started) < time.Millisecond {
		t.Fatal("timeout scenario returned without sleeping")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 after sleep", rec.Code)
	}
}
