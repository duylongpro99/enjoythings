package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestRunWritesLocalDevelopmentToken(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	userID := "6ed87f1f-7c9d-48d6-b23a-4d6255028c5c"
	now := time.Date(2099, 6, 2, 0, 0, 0, 0, time.UTC)

	err := run(
		[]string{"-user-id", userID, "-role", "tester", "-ttl", "30m"},
		func(key string) string {
			if key == "JWT_SECRET" {
				return "local-dev-secret"
			}
			return ""
		},
		&stdout,
		&stderr,
		func() time.Time { return now },
	)
	if err != nil {
		t.Fatalf("run devtoken: %v", err)
	}

	tokenString := strings.TrimSpace(stdout.String())
	claims := jwt.MapClaims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(*jwt.Token) (any, error) {
		return []byte("local-dev-secret"), nil
	})
	if err != nil {
		t.Fatalf("parse token: %v", err)
	}
	if !token.Valid {
		t.Fatal("token is invalid")
	}
	if claims["user_id"] != userID {
		t.Fatalf("user_id = %v, want %s", claims["user_id"], userID)
	}
	if claims["role"] != "tester" {
		t.Fatalf("role = %v, want tester", claims["role"])
	}
	if int64(claims["iat"].(float64)) != now.Unix() {
		t.Fatalf("iat = %v, want %d", claims["iat"], now.Unix())
	}
	if int64(claims["exp"].(float64)) != now.Add(30*time.Minute).Unix() {
		t.Fatalf("exp = %v, want %d", claims["exp"], now.Add(30*time.Minute).Unix())
	}
	if !strings.Contains(stderr.String(), "local development only") {
		t.Fatalf("stderr = %q, want local development warning", stderr.String())
	}
}

func TestRunRequiresJWTSecret(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	err := run(
		[]string{"-user-id", "6ed87f1f-7c9d-48d6-b23a-4d6255028c5c"},
		func(string) string { return "" },
		&stdout,
		&stderr,
		time.Now,
	)
	if err == nil {
		t.Fatal("expected missing JWT_SECRET to fail")
	}
}
