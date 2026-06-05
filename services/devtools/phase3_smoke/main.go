package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"enjoythings/services/internal/paymentprocessor"
	"enjoythings/services/internal/saga"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	defaultGatewayURL  = "http://localhost:8080"
	defaultDatabaseURL = "postgres://enjoythings:enjoythings_dev_password@localhost:5432/enjoythings?sslmode=disable"
	defaultJWTSecret   = "local-dev-jwt-secret-change-me"
)

type walletResponse struct {
	ID uuid.UUID `json:"id"`
}

type paymentResponse struct {
	PaymentID string `json:"payment_id"`
	Status    string `json:"status"`
}

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "phase3 smoke: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stdout, "phase3 smoke: ok")
}

func run(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("phase3_smoke", flag.ContinueOnError)
	var gatewayURL string
	var databaseURL string
	var jwtSecret string
	var timeout time.Duration
	fs.StringVar(&gatewayURL, "gateway-url", getenvDefault("GATEWAY_URL", defaultGatewayURL), "gateway base URL")
	fs.StringVar(&databaseURL, "database-url", getenvDefault("DATABASE_URL", defaultDatabaseURL), "Postgres URL for local smoke assertions")
	fs.StringVar(&jwtSecret, "jwt-secret", getenvDefault("JWT_SECRET", defaultJWTSecret), "local JWT secret")
	fs.DurationVar(&timeout, "timeout", 90*time.Second, "overall smoke timeout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if jwtSecret == "" {
		return errors.New("JWT secret is required")
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	gatewayURL = strings.TrimRight(gatewayURL, "/")
	client := &http.Client{Timeout: 5 * time.Second}
	if err := waitReady(ctx, client, gatewayURL); err != nil {
		return err
	}
	db, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return fmt.Errorf("connect smoke database: %w", err)
	}
	defer db.Close()

	userID := uuid.New()
	token, err := signToken(userID, jwtSecret)
	if err != nil {
		return err
	}
	from, to, err := createWalletPair(ctx, client, gatewayURL, token)
	if err != nil {
		return err
	}
	if err := setBalance(ctx, db, from.ID, 5000); err != nil {
		return err
	}
	if _, err := startPayment(ctx, client, gatewayURL, token, from.ID, to.ID, 1250, http.StatusUnprocessableEntity); err != nil {
		return fmt.Errorf("unverified gateway boundary: %w", err)
	}
	if err := submitVerification(ctx, client, gatewayURL, token); err != nil {
		return fmt.Errorf("verification boundary: %w", err)
	}

	if err := runScenario(ctx, client, db, gatewayURL, token, 1250, 5000, saga.StateCompleted, "CONFIRMED", saga.TopicTxCompleted, 1); err != nil {
		return fmt.Errorf("happy path boundary: %w", err)
	}
	if err := runScenario(ctx, client, db, gatewayURL, token, paymentprocessor.StubTerminalFailureAmountCents, 50000, saga.StateFailed, "CANCELED", saga.TopicTxFailed, 1); err != nil {
		return fmt.Errorf("terminal failure boundary: %w", err)
	}
	if err := runScenario(ctx, client, db, gatewayURL, token, paymentprocessor.StubRetryOnceAmountCents, 60000, saga.StateCompleted, "CONFIRMED", saga.TopicTxCompleted, 2); err != nil {
		return fmt.Errorf("payment retry boundary: %w", err)
	}
	return nil
}

func runScenario(ctx context.Context, client *http.Client, db *pgxpool.Pool, gatewayURL, token string, amount, initialBalance int64, sagaState, ledgerStatus, topic string, attempts int) error {
	from, to, err := createWalletPair(ctx, client, gatewayURL, token)
	if err != nil {
		return err
	}
	if err := setBalance(ctx, db, from.ID, initialBalance); err != nil {
		return err
	}
	payment, err := startPayment(ctx, client, gatewayURL, token, from.ID, to.ID, amount, http.StatusAccepted)
	if err != nil {
		return err
	}
	if err := waitPaymentState(ctx, client, gatewayURL, token, payment.PaymentID, sagaState); err != nil {
		return err
	}
	if err := waitDatabaseState(ctx, db, payment.PaymentID, ledgerStatus, topic, attempts); err != nil {
		return err
	}
	var balance int64
	if err := db.QueryRow(ctx, `SELECT balance FROM wallets WHERE id = $1`, from.ID).Scan(&balance); err != nil {
		return fmt.Errorf("read source balance: %w", err)
	}
	wantBalance := initialBalance - amount
	if sagaState == saga.StateFailed {
		wantBalance = initialBalance
	}
	if balance != wantBalance {
		return fmt.Errorf("wallet balance = %d, want %d", balance, wantBalance)
	}
	return nil
}

func createWalletPair(ctx context.Context, client *http.Client, gatewayURL, token string) (walletResponse, walletResponse, error) {
	from, err := createWallet(ctx, client, gatewayURL, token)
	if err != nil {
		return walletResponse{}, walletResponse{}, fmt.Errorf("create source wallet: %w", err)
	}
	to, err := createWallet(ctx, client, gatewayURL, token)
	if err != nil {
		return walletResponse{}, walletResponse{}, fmt.Errorf("create destination wallet: %w", err)
	}
	return from, to, nil
}

func createWallet(ctx context.Context, client *http.Client, gatewayURL, token string) (walletResponse, error) {
	var wallet walletResponse
	if err := doJSON(ctx, client, http.MethodPost, gatewayURL+"/v1/wallets", token, map[string]any{"currency": "USD"}, http.StatusCreated, &wallet); err != nil {
		return walletResponse{}, err
	}
	if wallet.ID == uuid.Nil {
		return walletResponse{}, errors.New("wallet response missing id")
	}
	return wallet, nil
}

func submitVerification(ctx context.Context, client *http.Client, gatewayURL, token string) error {
	var response struct {
		Status string `json:"status"`
	}
	if err := doJSON(ctx, client, http.MethodPost, gatewayURL+"/v1/verification/submit", token, map[string]any{
		"idempotency_key": uuid.NewString(),
		"trace_id":        uuid.NewString(),
	}, http.StatusOK, &response); err != nil {
		return err
	}
	if response.Status != "verified" {
		return fmt.Errorf("status = %s, want verified", response.Status)
	}
	return nil
}

func startPayment(ctx context.Context, client *http.Client, gatewayURL, token string, fromID, toID uuid.UUID, amount int64, wantStatus int) (paymentResponse, error) {
	var payment paymentResponse
	if err := doJSON(ctx, client, http.MethodPost, gatewayURL+"/v1/transfers", token, map[string]any{
		"payment_id":     uuid.NewString(),
		"from_wallet_id": fromID.String(),
		"to_wallet_id":   toID.String(),
		"amount_cents":   amount,
		"currency":       "USD",
	}, wantStatus, &payment); err != nil {
		return paymentResponse{}, err
	}
	return payment, nil
}

func waitPaymentState(ctx context.Context, client *http.Client, gatewayURL, token, paymentID, want string) error {
	return poll(ctx, func() (bool, error) {
		var payment paymentResponse
		if err := doJSON(ctx, client, http.MethodGet, gatewayURL+"/v1/payments/"+paymentID, token, nil, http.StatusOK, &payment); err != nil {
			return false, err
		}
		return payment.Status == want, nil
	}, "payment "+paymentID+" state "+want)
}

func waitDatabaseState(ctx context.Context, db *pgxpool.Pool, paymentID, ledgerStatus, topic string, attempts int) error {
	return poll(ctx, func() (bool, error) {
		var gotLedgerStatus string
		if err := db.QueryRow(ctx, `SELECT status FROM ledger_transfer_reservations WHERE payment_id = $1`, paymentID).Scan(&gotLedgerStatus); err != nil {
			return false, nil
		}
		var eventCount int
		if err := db.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE topic = $1 AND payload->>'payment_id' = $2`, topic, paymentID).Scan(&eventCount); err != nil {
			return false, err
		}
		var attemptCount int
		if err := db.QueryRow(ctx, `SELECT attempt_count FROM payment_attempts WHERE payment_id = $1`, paymentID).Scan(&attemptCount); err != nil {
			return false, nil
		}
		return gotLedgerStatus == ledgerStatus && eventCount > 0 && attemptCount == attempts, nil
	}, fmt.Sprintf("database payment=%s ledger=%s topic=%s attempt_count=%d", paymentID, ledgerStatus, topic, attempts))
}

func setBalance(ctx context.Context, db *pgxpool.Pool, walletID uuid.UUID, balance int64) error {
	_, err := db.Exec(ctx, `UPDATE wallets SET balance = $2, updated_at = now() WHERE id = $1`, walletID, balance)
	if err != nil {
		return fmt.Errorf("seed source wallet balance: %w", err)
	}
	return nil
}

func waitReady(ctx context.Context, client *http.Client, gatewayURL string) error {
	return poll(ctx, func() (bool, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, gatewayURL+"/readyz", nil)
		if err != nil {
			return false, err
		}
		resp, err := client.Do(req)
		if err != nil {
			return false, nil
		}
		defer resp.Body.Close()
		return resp.StatusCode == http.StatusOK, nil
	}, "gateway readiness")
}

func poll(ctx context.Context, check func() (bool, error), description string) error {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		ok, err := check()
		if err != nil {
			return err
		}
		if ok {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for %s: %w", description, ctx.Err())
		case <-ticker.C:
		}
	}
}

func doJSON(ctx context.Context, client *http.Client, method, url, token string, body any, wantStatus int, out any) error {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", uuid.NewString())
		req.Header.Set("X-Trace-Id", uuid.NewString())
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != wantStatus {
		return fmt.Errorf("%s %s status=%d want=%d body=%s", method, url, resp.StatusCode, wantStatus, strings.TrimSpace(string(responseBody)))
	}
	if out != nil && len(responseBody) > 0 {
		if err := json.Unmarshal(responseBody, out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

func signToken(userID uuid.UUID, secret string) (string, error) {
	now := time.Now().UTC()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"user_id": userID.String(),
		"role":    "user",
		"iat":     now.Unix(),
		"exp":     now.Add(time.Hour).Unix(),
	})
	signed, err := token.SignedString([]byte(secret))
	if err != nil {
		return "", fmt.Errorf("sign token: %w", err)
	}
	return signed, nil
}

func getenvDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
