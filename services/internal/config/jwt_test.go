package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadGatewayDefaultsToHS256AndRequiresTheSecret(t *testing.T) {
	base := map[string]string{"JWT_SECRET": "local-secret"}

	cfg, err := LoadGatewayFromLookup(lookupFrom(base))
	if err != nil {
		t.Fatalf("LoadGateway: %v", err)
	}
	if cfg.JWTAlgorithm != "HS256" || cfg.JWTSecret != "local-secret" {
		t.Fatalf("config = %s/%q, want HS256 with the secret", cfg.JWTAlgorithm, cfg.JWTSecret)
	}

	if _, err := LoadGatewayFromLookup(lookupFrom(map[string]string{})); err == nil {
		t.Fatal("a gateway without JWT_SECRET was accepted")
	}
}

func TestLoadGatewayAcceptsRS256WithAnInlinePublicKey(t *testing.T) {
	cfg, err := LoadGatewayFromLookup(lookupFrom(map[string]string{
		"JWT_ALG":            "RS256",
		"JWT_PUBLIC_KEY_PEM": "-----BEGIN PUBLIC KEY-----\nMIIB\n-----END PUBLIC KEY-----\n",
	}))
	if err != nil {
		t.Fatalf("LoadGateway: %v", err)
	}
	if cfg.JWTAlgorithm != "RS256" {
		t.Fatalf("algorithm = %q, want RS256", cfg.JWTAlgorithm)
	}
	if !strings.Contains(cfg.JWTPublicKeyPEM, "BEGIN PUBLIC KEY") {
		t.Fatalf("public key = %q, want the configured PEM", cfg.JWTPublicKeyPEM)
	}
	if cfg.JWTSecret != "" {
		t.Fatalf("secret = %q, want empty under RS256", cfg.JWTSecret)
	}
}

func TestLoadGatewayReadsTheRS256KeyFromAFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jwt.pub")
	if err := os.WriteFile(path, []byte("-----BEGIN PUBLIC KEY-----\nfromfile\n-----END PUBLIC KEY-----\n"), 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}

	cfg, err := LoadGatewayFromLookup(lookupFrom(map[string]string{
		"JWT_ALG":             "RS256",
		"JWT_PUBLIC_KEY_FILE": path,
		"JWT_PUBLIC_KEY_PEM":  "-----BEGIN PUBLIC KEY-----\ninline\n-----END PUBLIC KEY-----\n",
	}))
	if err != nil {
		t.Fatalf("LoadGateway: %v", err)
	}
	// The file wins: a mounted secret is the operational path, the inline
	// value is the convenience one.
	if !strings.Contains(cfg.JWTPublicKeyPEM, "fromfile") {
		t.Fatalf("public key = %q, want the file contents", cfg.JWTPublicKeyPEM)
	}
}

func TestLoadGatewayRejectsIncompleteOrUnknownJWTSettings(t *testing.T) {
	if _, err := LoadGatewayFromLookup(lookupFrom(map[string]string{"JWT_ALG": "RS256"})); err == nil {
		t.Fatal("RS256 without a public key was accepted")
	}
	if _, err := LoadGatewayFromLookup(lookupFrom(map[string]string{
		"JWT_ALG":             "RS256",
		"JWT_PUBLIC_KEY_FILE": filepath.Join(t.TempDir(), "missing.pub"),
	})); err == nil {
		t.Fatal("a missing key file was accepted")
	}
	if _, err := LoadGatewayFromLookup(lookupFrom(map[string]string{
		"JWT_ALG":    "none",
		"JWT_SECRET": "local-secret",
	})); err == nil {
		t.Fatal("an unsupported algorithm was accepted")
	}
}

func TestLoadServiceConfigSharesTheJWTSettings(t *testing.T) {
	cfg, err := LoadFromLookup(lookupFrom(map[string]string{
		"DATABASE_URL":       "postgres://localhost:5432/enjoythings",
		"JWT_ALG":            "RS256",
		"JWT_PUBLIC_KEY_PEM": "-----BEGIN PUBLIC KEY-----\nMIIB\n-----END PUBLIC KEY-----\n",
	}))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.JWTAlgorithm != "RS256" || cfg.JWTPublicKeyPEM == "" {
		t.Fatalf("config = %s/%q, want RS256 with a key", cfg.JWTAlgorithm, cfg.JWTPublicKeyPEM)
	}
}

func lookupFrom(values map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		value, ok := values[key]
		return value, ok
	}
}
