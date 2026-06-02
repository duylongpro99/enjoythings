package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

type readinessFunc func(context.Context) error

func (fn readinessFunc) Ping(ctx context.Context) error {
	return fn(ctx)
}

func TestRouterRegistersHealthEndpoints(t *testing.T) {
	router := NewRouter(readinessFunc(func(context.Context) error { return nil }), "test-jwt-secret")

	tests := []struct {
		name   string
		path   string
		status int
		body   string
	}{
		{
			name:   "health",
			path:   "/healthz",
			status: http.StatusOK,
			body:   "{\"status\":\"ok\"}\n",
		},
		{
			name:   "ready",
			path:   "/readyz",
			status: http.StatusOK,
			body:   "{\"status\":\"ready\"}\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			rec := httptest.NewRecorder()

			router.ServeHTTP(rec, req)

			if rec.Code != tt.status {
				t.Fatalf("status = %d, want %d", rec.Code, tt.status)
			}
			if got := rec.Body.String(); got != tt.body {
				t.Fatalf("body = %q, want %q", got, tt.body)
			}
		})
	}
}

func TestRouterReadyEndpointReturnsUnavailable(t *testing.T) {
	router := NewRouter(readinessFunc(func(context.Context) error {
		return errors.New("database unavailable")
	}), "test-jwt-secret")
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestRouterRequiresAuthenticationForV1Routes(t *testing.T) {
	router := NewRouter(readinessFunc(func(context.Context) error { return nil }), "test-jwt-secret")
	req := httptest.NewRequest(http.MethodPost, "/v1/wallets", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	wantBody := "{\"error\":{\"code\":\"unauthorized\",\"message\":\"authentication required\"}}\n"
	if got := rec.Body.String(); got != wantBody {
		t.Fatalf("body = %q, want %q", got, wantBody)
	}
}

func TestRouterAllowsAuthenticatedV1RequestsToReachBusinessRouter(t *testing.T) {
	router := NewRouter(readinessFunc(func(context.Context) error { return nil }), "test-jwt-secret")
	req := httptest.NewRequest(http.MethodPost, "/v1/wallets", nil)
	req.Header.Set("Authorization", "Bearer "+routerTestToken(t))
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func routerTestToken(t *testing.T) string {
	t.Helper()

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"user_id": uuid.New().String(),
		"role":    "user",
		"exp":     time.Now().Add(time.Hour).Unix(),
	})
	signed, err := token.SignedString([]byte("test-jwt-secret"))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return signed
}
