package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"enjoythings/services/internal/auth"

	"github.com/google/uuid"
)

func TestRateLimiterAllowsRequestsWithinBurst(t *testing.T) {
	userID := uuid.New()
	calls := 0
	limiter := NewRateLimiter(2, time.Minute)
	handler := limiter.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls++
	}))

	for range 2 {
		req := requestWithPrincipal(userID)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d; body %s", rec.Code, http.StatusOK, rec.Body.String())
		}
	}

	if calls != 2 {
		t.Fatalf("calls = %d, want 2", calls)
	}
}

func TestRateLimiterReturns429AfterTokenExhaustion(t *testing.T) {
	userID := uuid.New()
	calls := 0
	limiter := NewRateLimiter(1, time.Hour)
	handler := limiter.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls++
	}))

	handler.ServeHTTP(httptest.NewRecorder(), requestWithPrincipal(userID))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, requestWithPrincipal(userID))

	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d; body %s", rec.Code, http.StatusTooManyRequests, rec.Body.String())
	}
	want := "{\"error\":{\"code\":\"rate_limited\",\"message\":\"rate limit exceeded\"}}\n"
	if got := rec.Body.String(); got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}

func requestWithPrincipal(userID uuid.UUID) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/v1/wallets/"+uuid.NewString(), nil)
	return req.WithContext(auth.ContextWithPrincipal(req.Context(), auth.Principal{UserID: userID, Role: "user"}))
}
