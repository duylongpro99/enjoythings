package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const testSecret = "test-jwt-secret"

func TestMiddlewareRejectsInvalidAuthentication(t *testing.T) {
	userID := uuid.New()
	validClaims := map[string]any{
		"user_id": userID.String(),
		"role":    "user",
		"exp":     time.Now().Add(time.Hour).Unix(),
	}

	tests := []struct {
		name          string
		authorization string
	}{
		{name: "missing header"},
		{name: "non bearer scheme", authorization: "Basic abc123"},
		{name: "malformed bearer token", authorization: "Bearer not-a-jwt"},
		{name: "wrong signature", authorization: "Bearer " + signToken(t, "wrong-secret", validClaims)},
		{name: "expired token", authorization: "Bearer " + signToken(t, testSecret, map[string]any{
			"user_id": userID.String(),
			"role":    "user",
			"exp":     time.Now().Add(-time.Minute).Unix(),
		})},
		{name: "missing user id", authorization: "Bearer " + signToken(t, testSecret, map[string]any{
			"role": "user",
			"exp":  time.Now().Add(time.Hour).Unix(),
		})},
		{name: "subject without user id", authorization: "Bearer " + signToken(t, testSecret, map[string]any{
			"sub":  userID.String(),
			"role": "user",
			"exp":  time.Now().Add(time.Hour).Unix(),
		})},
		{name: "missing role", authorization: "Bearer " + signToken(t, testSecret, map[string]any{
			"user_id": userID.String(),
			"exp":     time.Now().Add(time.Hour).Unix(),
		})},
		{name: "invalid user id", authorization: "Bearer " + signToken(t, testSecret, map[string]any{
			"user_id": "not-a-uuid",
			"role":    "user",
			"exp":     time.Now().Add(time.Hour).Unix(),
		})},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			handler := Middleware(testSecret)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				called = true
			}))
			req := httptest.NewRequest(http.MethodGet, "/v1/wallets", nil)
			if tt.authorization != "" {
				req.Header.Set("Authorization", tt.authorization)
			}
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if called {
				t.Fatal("next handler was called")
			}
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
			}
			wantBody := "{\"error\":{\"code\":\"unauthorized\",\"message\":\"authentication required\"}}\n"
			if got := rec.Body.String(); got != wantBody {
				t.Fatalf("body = %q, want %q", got, wantBody)
			}
		})
	}
}

func TestMiddlewareInjectsPrincipalForValidToken(t *testing.T) {
	userID := uuid.New()
	token := signToken(t, testSecret, map[string]any{
		"user_id": userID.String(),
		"role":    "user",
		"exp":     time.Now().Add(time.Hour).Unix(),
		"iat":     time.Now().Unix(),
	})
	var principal Principal
	handler := Middleware(testSecret)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		var ok bool
		principal, ok = PrincipalFromContext(r.Context())
		if !ok {
			t.Fatal("principal missing from context")
		}
	}))
	req := httptest.NewRequest(http.MethodGet, "/v1/wallets", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if principal.UserID != userID {
		t.Fatalf("UserID = %s, want %s", principal.UserID, userID)
	}
	if principal.Role != "user" {
		t.Fatalf("Role = %q, want user", principal.Role)
	}
}

func TestPrincipalFromContextReturnsFalseWhenMissing(t *testing.T) {
	if _, ok := PrincipalFromContext(context.Background()); ok {
		t.Fatal("principal unexpectedly found")
	}
}

func signToken(t *testing.T, secret string, claims map[string]any) string {
	t.Helper()

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims(claims))
	signed, err := token.SignedString([]byte(secret))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return signed
}
