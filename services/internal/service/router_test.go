package service

import (
	"bytes"
	"context"
	"enjoythings/services/internal/auth"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"enjoythings/services/internal/domain"
	"enjoythings/services/internal/repo"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

type readinessFunc func(context.Context) error

func (fn readinessFunc) Ping(ctx context.Context) error {
	return fn(ctx)
}

func TestRouterRegistersHealthEndpoints(t *testing.T) {
	router := NewRouter(readinessFunc(func(context.Context) error { return nil }), &routerStore{}, auth.HMACVerifier("test-jwt-secret"))

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
	}), &routerStore{}, auth.HMACVerifier("test-jwt-secret"))
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestRouterRequiresAuthenticationForV1Routes(t *testing.T) {
	router := NewRouter(readinessFunc(func(context.Context) error { return nil }), &routerStore{}, auth.HMACVerifier("test-jwt-secret"))
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

func TestRouterAllowsAuthenticatedV1RequestsToReachBusinessHandlers(t *testing.T) {
	store := &routerStore{}
	router := NewRouter(readinessFunc(func(context.Context) error { return nil }), store, auth.HMACVerifier("test-jwt-secret"))
	req := httptest.NewRequest(http.MethodPost, "/v1/wallets", bytes.NewBufferString(`{"currency":"USD"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+routerTestToken(t))
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body %s", rec.Code, http.StatusCreated, rec.Body.String())
	}
	if store.createCalled != 1 {
		t.Fatalf("create calls = %d, want 1", store.createCalled)
	}
}

func TestRouterUnknownV1RouteUsesErrorEnvelope(t *testing.T) {
	router := NewRouter(readinessFunc(func(context.Context) error { return nil }), &routerStore{}, auth.HMACVerifier("test-jwt-secret"))
	req := httptest.NewRequest(http.MethodGet, "/v1/unknown", nil)
	req.Header.Set("Authorization", "Bearer "+routerTestToken(t))
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	wantBody := "{\"error\":{\"code\":\"not_found\",\"message\":\"resource not found\"}}\n"
	if got := rec.Body.String(); got != wantBody {
		t.Fatalf("body = %q, want %q", got, wantBody)
	}
}

type routerStore struct {
	createCalled int
}

func (store *routerStore) CreateWallet(_ context.Context, userID uuid.UUID, currency string) (domain.Wallet, error) {
	store.createCalled++
	now := time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
	return domain.Wallet{ID: uuid.New(), UserID: userID, Currency: currency, CreatedAt: now, UpdatedAt: now}, nil
}

func (store *routerStore) GetWallet(context.Context, uuid.UUID) (domain.Wallet, error) {
	return domain.Wallet{}, domain.ErrNotFound
}

func (store *routerStore) CreateTransfer(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, int64) (domain.Transfer, error) {
	return domain.Transfer{}, domain.ErrNotFound
}

func (store *routerStore) DebitForSaga(context.Context, domain.SagaDebitCommand) (domain.SagaWalletOperation, error) {
	return domain.SagaWalletOperation{}, domain.ErrNotFound
}

func (store *routerStore) CompensateDebit(context.Context, domain.SagaCompensationCommand) (domain.SagaWalletOperation, error) {
	return domain.SagaWalletOperation{}, domain.ErrNotFound
}

func (store *routerStore) ListLedgerEntries(context.Context, uuid.UUID, repo.LedgerCursor, int) ([]domain.LedgerEntry, repo.LedgerCursor, error) {
	return nil, repo.LedgerCursor{}, nil
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
