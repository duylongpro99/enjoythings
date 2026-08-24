package auth

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// Supported token algorithms. HS256 keeps local development to a single shared
// secret; RS256 lets an external issuer hold the private key so the services
// only ever need the public half.
const (
	AlgHS256 = "HS256"
	AlgRS256 = "RS256"
)

// ErrUnsupportedAlgorithm names an algorithm the services will not accept.
// Refusing anything outside the list is what prevents an "alg" downgrade.
var ErrUnsupportedAlgorithm = errors.New("unsupported jwt algorithm")

// Verifier holds the key material for one signing algorithm. It is pinned to a
// single method so a token signed with a different one is rejected before any
// claim is read.
type Verifier struct {
	method jwt.SigningMethod
	key    any
}

// HMACVerifier accepts HS256 tokens signed with the shared secret.
func HMACVerifier(secret string) Verifier {
	return Verifier{method: jwt.SigningMethodHS256, key: []byte(secret)}
}

// RSAVerifier accepts RS256 tokens signed by the holder of the matching
// private key. The PEM is the public half only.
func RSAVerifier(publicKeyPEM string) (Verifier, error) {
	key, err := jwt.ParseRSAPublicKeyFromPEM([]byte(publicKeyPEM))
	if err != nil {
		return Verifier{}, err
	}
	return Verifier{method: jwt.SigningMethodRS256, key: key}, nil
}

// NewVerifier builds the verifier described by configuration.
func NewVerifier(algorithm, secret, publicKeyPEM string) (Verifier, error) {
	switch strings.ToUpper(strings.TrimSpace(algorithm)) {
	case "", AlgHS256:
		return HMACVerifier(secret), nil
	case AlgRS256:
		return RSAVerifier(publicKeyPEM)
	default:
		return Verifier{}, ErrUnsupportedAlgorithm
	}
}

// Algorithm names the accepted signing method.
func (verifier Verifier) Algorithm() string {
	if verifier.method == nil {
		return ""
	}
	return verifier.method.Alg()
}

func (verifier Verifier) keyFunc(token *jwt.Token) (any, error) {
	if verifier.method == nil || token.Method.Alg() != verifier.method.Alg() {
		return nil, authError("invalid_signing_method")
	}
	if _, ok := verifier.key.(*rsa.PublicKey); ok && verifier.method.Alg() != AlgRS256 {
		return nil, authError("invalid_signing_method")
	}
	return verifier.key, nil
}

type Principal struct {
	UserID uuid.UUID
	Role   string
}

type contextKey struct{}

func PrincipalFromContext(ctx context.Context) (Principal, bool) {
	principal, ok := ctx.Value(contextKey{}).(Principal)
	return principal, ok
}

func ContextWithPrincipal(ctx context.Context, principal Principal) context.Context {
	return context.WithValue(ctx, contextKey{}, principal)
}

// Middleware verifies HS256 tokens. It remains the local-development path and
// the default for services configured with a shared secret.
func Middleware(secret string) func(http.Handler) http.Handler {
	return VerifierMiddleware(HMACVerifier(secret))
}

// VerifierMiddleware verifies tokens with any configured verifier.
func VerifierMiddleware(verifier Verifier) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal, err := authenticate(r.Header.Get("Authorization"), verifier)
			if err != nil {
				slog.Debug("authentication failed", "category", err.Error())
				writeUnauthorized(w)
				return
			}

			ctx := ContextWithPrincipal(r.Context(), principal)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func authenticate(header string, verifier Verifier) (Principal, error) {
	if header == "" {
		return Principal{}, authError("missing_authorization")
	}

	scheme, tokenString, ok := strings.Cut(header, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || tokenString == "" || strings.Contains(tokenString, " ") {
		return Principal{}, authError("invalid_authorization_format")
	}

	claims := jwt.MapClaims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, verifier.keyFunc,
		jwt.WithExpirationRequired(),
		jwt.WithValidMethods([]string{verifier.Algorithm()}),
	)
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
