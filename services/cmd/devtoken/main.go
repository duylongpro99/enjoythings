package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

func main() {
	if err := run(os.Args[1:], os.Getenv, os.Stdout, os.Stderr, time.Now); err != nil {
		fmt.Fprintf(os.Stderr, "devtoken: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, getenv func(string) string, stdout io.Writer, stderr io.Writer, now func() time.Time) error {
	fs := flag.NewFlagSet("devtoken", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var userID string
	var role string
	var ttl time.Duration
	fs.StringVar(&userID, "user-id", "", "UUID user_id claim")
	fs.StringVar(&role, "role", "user", "role claim")
	fs.DurationVar(&ttl, "ttl", time.Hour, "token lifetime")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if _, err := uuid.Parse(userID); err != nil {
		return errors.New("-user-id must be a UUID")
	}
	if role == "" {
		return errors.New("-role is required")
	}
	if ttl <= 0 {
		return errors.New("-ttl must be positive")
	}

	secret := getenv("JWT_SECRET")
	if secret == "" {
		return errors.New("JWT_SECRET is required")
	}

	issuedAt := now().UTC()
	claims := jwt.MapClaims{
		"user_id": userID,
		"role":    role,
		"iat":     issuedAt.Unix(),
		"exp":     issuedAt.Add(ttl).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(secret))
	if err != nil {
		return fmt.Errorf("sign token: %w", err)
	}

	if _, err := fmt.Fprintln(stderr, "local development only: do not commit or share generated tokens"); err != nil {
		return fmt.Errorf("write warning: %w", err)
	}
	if _, err := fmt.Fprintln(stdout, signed); err != nil {
		return fmt.Errorf("write token: %w", err)
	}
	return nil
}
