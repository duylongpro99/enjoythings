package handler

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

type readyFunc func(context.Context) error

func (fn readyFunc) Ping(ctx context.Context) error {
	return fn(ctx)
}

func TestHealthReturnsOKWithoutReadinessDependency(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	Health().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Body.String(); got != "{\"status\":\"ok\"}\n" {
		t.Fatalf("body = %q", got)
	}
}

func TestHealthRejectsUnsupportedMethodsWithErrorEnvelope(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/healthz", nil)
	rec := httptest.NewRecorder()

	Health().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	want := "{\"error\":{\"code\":\"not_found\",\"message\":\"resource not found\"}}\n"
	if got := rec.Body.String(); got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func TestReadyReturnsOKWhenDatabasePings(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	checker := readyFunc(func(context.Context) error { return nil })

	Ready(checker).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Body.String(); got != "{\"status\":\"ready\"}\n" {
		t.Fatalf("body = %q", got)
	}
}

func TestReadyRejectsUnsupportedMethodsWithErrorEnvelope(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/readyz", nil)
	rec := httptest.NewRecorder()
	checker := readyFunc(func(context.Context) error { return nil })

	Ready(checker).ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	want := "{\"error\":{\"code\":\"not_found\",\"message\":\"resource not found\"}}\n"
	if got := rec.Body.String(); got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func TestReadyReturnsServiceUnavailableWhenDatabasePingFails(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	checker := readyFunc(func(context.Context) error { return errors.New("connection refused") })

	Ready(checker).ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
	want := "{\"error\":{\"code\":\"service_unavailable\",\"message\":\"database is not ready\"}}\n"
	if got := rec.Body.String(); got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}
