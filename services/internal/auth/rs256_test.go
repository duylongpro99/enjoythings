package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

func TestRS256VerifierAcceptsTokensFromTheIssuerKey(t *testing.T) {
	key := newRSAKey(t)
	verifier, err := RSAVerifier(publicKeyPEM(t, key))
	if err != nil {
		t.Fatalf("RSAVerifier: %v", err)
	}
	userID := uuid.New()

	principal, err := authenticate("Bearer "+signRS256(t, key, jwt.MapClaims{
		"user_id": userID.String(),
		"role":    "admin",
		"exp":     time.Now().Add(time.Hour).Unix(),
	}), verifier)
	if err != nil {
		t.Fatalf("authenticate: %v", err)
	}
	if principal.UserID != userID || principal.Role != "admin" {
		t.Fatalf("principal = %+v, want %s/admin", principal, userID)
	}
}

func TestRS256VerifierRejectsHS256TokensSignedWithThePublicKey(t *testing.T) {
	// The classic algorithm-confusion attack: sign HS256 using the RSA public
	// key as the shared secret and hope the server picks the algorithm from
	// the token header.
	key := newRSAKey(t)
	publicPEM := publicKeyPEM(t, key)
	verifier, err := RSAVerifier(publicPEM)
	if err != nil {
		t.Fatalf("RSAVerifier: %v", err)
	}
	forged := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"user_id": uuid.NewString(),
		"role":    "admin",
		"exp":     time.Now().Add(time.Hour).Unix(),
	})
	signed, err := forged.SignedString([]byte(publicPEM))
	if err != nil {
		t.Fatalf("sign forged token: %v", err)
	}

	if _, err := authenticate("Bearer "+signed, verifier); err == nil {
		t.Fatal("an HS256 token was accepted by the RS256 verifier")
	}
}

func TestRS256VerifierRejectsAnotherIssuersKey(t *testing.T) {
	verifier, err := RSAVerifier(publicKeyPEM(t, newRSAKey(t)))
	if err != nil {
		t.Fatalf("RSAVerifier: %v", err)
	}

	_, err = authenticate("Bearer "+signRS256(t, newRSAKey(t), jwt.MapClaims{
		"user_id": uuid.NewString(),
		"role":    "user",
		"exp":     time.Now().Add(time.Hour).Unix(),
	}), verifier)
	if err == nil {
		t.Fatal("a token from a different key was accepted")
	}
}

func TestHMACVerifierRejectsRS256Tokens(t *testing.T) {
	key := newRSAKey(t)

	_, err := authenticate("Bearer "+signRS256(t, key, jwt.MapClaims{
		"user_id": uuid.NewString(),
		"role":    "user",
		"exp":     time.Now().Add(time.Hour).Unix(),
	}), HMACVerifier("test-jwt-secret"))
	if err == nil {
		t.Fatal("an RS256 token was accepted by the HS256 verifier")
	}
}

func TestNewVerifierSelectsTheConfiguredAlgorithm(t *testing.T) {
	key := newRSAKey(t)

	hmac, err := NewVerifier("", "secret", "")
	if err != nil || hmac.Algorithm() != AlgHS256 {
		t.Fatalf("default verifier = %q, %v; want %s", hmac.Algorithm(), err, AlgHS256)
	}
	rsaVerifier, err := NewVerifier("rs256", "", publicKeyPEM(t, key))
	if err != nil || rsaVerifier.Algorithm() != AlgRS256 {
		t.Fatalf("rs256 verifier = %q, %v; want %s", rsaVerifier.Algorithm(), err, AlgRS256)
	}
	if _, err := NewVerifier("none", "", ""); !errors.Is(err, ErrUnsupportedAlgorithm) {
		t.Fatalf("alg none error = %v, want %v", err, ErrUnsupportedAlgorithm)
	}
	if _, err := NewVerifier(AlgRS256, "", "not a pem"); err == nil {
		t.Fatal("an invalid public key was accepted")
	}
}

func TestVerifierMiddlewareGuardsRoutesWithRS256(t *testing.T) {
	key := newRSAKey(t)
	verifier, err := RSAVerifier(publicKeyPEM(t, key))
	if err != nil {
		t.Fatalf("RSAVerifier: %v", err)
	}
	called := false
	handler := VerifierMiddleware(verifier)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))

	req := httptest.NewRequest(http.MethodGet, "/v1/wallets", nil)
	req.Header.Set("Authorization", "Bearer "+signRS256(t, key, jwt.MapClaims{
		"user_id": uuid.NewString(),
		"role":    "user",
		"exp":     time.Now().Add(time.Hour).Unix(),
	}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if !called || rec.Code != http.StatusOK {
		t.Fatalf("valid RS256 request: called=%v status=%d", called, rec.Code)
	}

	called = false
	unsigned := httptest.NewRequest(http.MethodGet, "/v1/wallets", nil)
	unsigned.Header.Set("Authorization", "Bearer not-a-jwt")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, unsigned)
	if called || rec.Code != http.StatusUnauthorized {
		t.Fatalf("invalid request: called=%v status=%d, want 401", called, rec.Code)
	}
}

func newRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return key
}

func publicKeyPEM(t *testing.T, key *rsa.PrivateKey) string {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatalf("marshal public key: %v", err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

func signRS256(t *testing.T, key *rsa.PrivateKey, claims jwt.MapClaims) string {
	t.Helper()
	signed, err := jwt.NewWithClaims(jwt.SigningMethodRS256, claims).SignedString(key)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return signed
}
