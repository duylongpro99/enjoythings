package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type readinessFunc func(context.Context) error

func (fn readinessFunc) Ping(ctx context.Context) error {
	return fn(ctx)
}

func TestRouterRegistersHealthEndpoints(t *testing.T) {
	router := NewRouter(readinessFunc(func(context.Context) error { return nil }))

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
	}))
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}
