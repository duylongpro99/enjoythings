package auth

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

type Principal struct {
	UserID uuid.UUID
	Role   string
}

type contextKey struct{}

func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(contextKey{}).(Principal)
	return principal, ok
}

func Middleware(secret string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal, err := authenticate(r.Header.Get("Authorization"), secret)
			if err != nil {
				slog.Debug("authentication failed", "category", err.Error())
				writeUnauthorized(w)
				return
			}

			ctx := context.WithValue(r.Context(), contextKey{}, principal)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func authenticate(header string, secret string) (Principal, error) {
	if header == "" {
		return Principal{}, authError("missing_authorization")
	}

	scheme, tokenString, ok := strings.Cut(header, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || tokenString == "" || strings.Contains(tokenString, " ") {
		return Principal{}, authError("invalid_authorization_format")
	}

	claims := jwt.MapClaims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, authError("invalid_signing_method")
		}
		return []byte(secret), nil
	}, jwt.WithExpirationRequired())
	if err != nil || !token.Valid {
		return Principal{}, authError("invalid_token")
	}

	userIDRaw, ok := claims["user_id"].(string)
	if !ok || userIDRaw == "" {
		return Principal{}, authError("missing_user_id")
	}
	userID, err := uuid.Parse(userIDRaw)
	if err != nil {
		return Principal{}, authError("invalid_user_id")
	}

	role, ok := claims["role"].(string)
	if !ok || role == "" {
		return Principal{}, authError("missing_role")
	}

	return Principal{UserID: userID, Role: role}, nil
}

type authError string

func (err authError) Error() string {
	return string(err)
}

func writeUnauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]string{
			"code":    "unauthorized",
			"message": "authentication required",
		},
	})
}
