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

	"enjoythings/services/internal/repo"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const (
	defaultGatewayURL  = "http://localhost:8080"
	defaultDatabaseURL = "postgres://enjoythings:enjoythings_dev_password@localhost:5432/enjoythings?sslmode=disable"
	defaultJWTSecret   = "local-dev-jwt-secret-change-me"
)

type walletResponse struct {
	ID       uuid.UUID `json:"id"`
	UserID   uuid.UUID `json:"user_id"`
	Balance  int64     `json:"balance"`
	Currency string    `json:"currency"`
}

type transferResponse struct {
	ID uuid.UUID `json:"id"`
}

type ledgerResponse struct {
	Entries []ledgerEntry `json:"entries"`
}

type ledgerEntry struct {
	TransferID uuid.UUID `json:"transfer_id"`
	Direction  string    `json:"direction"`
	Amount     int64     `json:"amount"`
}

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "phase2 smoke: %v\n", err)
		os.Exit(1)
	}
	_, _ = fmt.Fprintln(os.Stdout, "phase2 smoke: ok")
}

func run(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("phase2_smoke", flag.ContinueOnError)
	var gatewayURL string
	var databaseURL string
	var jwtSecret string
	var timeout time.Duration
	var userIDRaw string
	var skipLedgerWait bool
	var expectLedgerWalletIDRaw string
	var expectLedgerTransferIDRaw string
	fs.StringVar(&gatewayURL, "gateway-url", getenvDefault("GATEWAY_URL", defaultGatewayURL), "gateway base URL")
	fs.StringVar(&databaseURL, "database-url", getenvDefault("DATABASE_URL", defaultDatabaseURL), "Postgres URL for local smoke seeding")
	fs.StringVar(&jwtSecret, "jwt-secret", getenvDefault("JWT_SECRET", defaultJWTSecret), "local JWT secret")
	fs.DurationVar(&timeout, "timeout", 45*time.Second, "overall smoke timeout")
	fs.StringVar(&userIDRaw, "user-id", "", "optional fixed user UUID")
	fs.BoolVar(&skipLedgerWait, "skip-ledger-wait", false, "create a transfer but do not wait for ledger consumption")
	fs.StringVar(&expectLedgerWalletIDRaw, "expect-ledger-wallet-id", "", "wallet UUID to poll for an existing ledger entry")
	fs.StringVar(&expectLedgerTransferIDRaw, "expect-ledger-transfer-id", "", "transfer UUID to poll for an existing ledger entry")
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
	if err := expectStatus(ctx, client, http.MethodPost, gatewayURL+"/v1/wallets", "invalid-token", map[string]any{"currency": "USD"}, http.StatusUnauthorized); err != nil {
		return fmt.Errorf("invalid JWT check: %w", err)
	}

	userID := uuid.New()
	if userIDRaw != "" {
		parsed, err := uuid.Parse(userIDRaw)
		if err != nil {
			return fmt.Errorf("parse -user-id: %w", err)
		}
		userID = parsed
	}
	token, err := signToken(userID, jwtSecret, time.Now)
	if err != nil {
		return err
	}
	if expectLedgerWalletIDRaw != "" || expectLedgerTransferIDRaw != "" {
		walletID, err := uuid.Parse(expectLedgerWalletIDRaw)
		if err != nil {
			return fmt.Errorf("parse -expect-ledger-wallet-id: %w", err)
		}
		transferID, err := uuid.Parse(expectLedgerTransferIDRaw)
		if err != nil {
			return fmt.Errorf("parse -expect-ledger-transfer-id: %w", err)
		}
		return waitLedgerEntry(ctx, client, gatewayURL, token, walletID, transferID, "debit", 1250)
	}

	from, err := createWallet(ctx, client, gatewayURL, token)
	if err != nil {
		return fmt.Errorf("create source wallet: %w", err)
	}
	to, err := createWallet(ctx, client, gatewayURL, token)
	if err != nil {
		return fmt.Errorf("create destination wallet: %w", err)
	}

	db, err := repo.Connect(ctx, databaseURL, 5)
	if err != nil {
		return fmt.Errorf("connect smoke database: %w", err)
	}
	defer db.Close()
	if err := db.SetWalletBalanceForTest(ctx, from.ID, 5000); err != nil {
		return fmt.Errorf("seed source wallet balance: %w", err)
	}

	if err := expectStatus(ctx, client, http.MethodPost, gatewayURL+"/v1/transfers", token, map[string]any{
		"from_wallet_id": from.ID.String(),
		"to_wallet_id":   to.ID.String(),
		"amount":         6000,
	}, http.StatusUnprocessableEntity); err != nil {
		return fmt.Errorf("insufficient funds check: %w", err)
	}

	transfer, err := createTransfer(ctx, client, gatewayURL, token, from.ID, to.ID, 1250)
	if err != nil {
		return fmt.Errorf("create transfer: %w", err)
	}
	_, _ = fmt.Fprintf(os.Stdout, "phase2 smoke: user_id=%s from_wallet_id=%s transfer_id=%s\n", userID, from.ID, transfer.ID)
	if skipLedgerWait {
		return nil
	}
	if err := waitLedgerEntry(ctx, client, gatewayURL, token, from.ID, transfer.ID, "debit", 1250); err != nil {
		return err
	}
	return nil
}

func waitReady(ctx context.Context, client *http.Client, gatewayURL string) error {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, gatewayURL+"/readyz", nil)
		if err != nil {
			return err
		}
		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("gateway readiness timeout: %w", ctx.Err())
		case <-ticker.C:
		}
	}
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

func createTransfer(ctx context.Context, client *http.Client, gatewayURL, token string, fromID, toID uuid.UUID, amount int64) (transferResponse, error) {
	var transfer transferResponse
	if err := doJSON(ctx, client, http.MethodPost, gatewayURL+"/v1/transfers", token, map[string]any{
		"from_wallet_id": fromID.String(),
		"to_wallet_id":   toID.String(),
		"amount":         amount,
	}, http.StatusCreated, &transfer); err != nil {
		return transferResponse{}, err
	}
	if transfer.ID == uuid.Nil {
		return transferResponse{}, errors.New("transfer response missing id")
	}
	return transfer, nil
}

func waitLedgerEntry(ctx context.Context, client *http.Client, gatewayURL, token string, walletID, transferID uuid.UUID, direction string, amount int64) error {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		var ledger ledgerResponse
		if err := doJSON(ctx, client, http.MethodGet, gatewayURL+"/v1/ledger/"+walletID.String()+"?limit=50", token, nil, http.StatusOK, &ledger); err != nil {
			return fmt.Errorf("query ledger: %w", err)
		}
		for _, entry := range ledger.Entries {
			if entry.TransferID == transferID && entry.Direction == direction && entry.Amount == amount {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for ledger entry transfer_id=%s wallet_id=%s: %w", transferID, walletID, ctx.Err())
		case <-ticker.C:
		}
	}
}

func expectStatus(ctx context.Context, client *http.Client, method, url, token string, body any, want int) error {
	return doJSON(ctx, client, method, url, token, body, want, nil)
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
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != wantStatus {
		return fmt.Errorf("%s %s status=%d want=%d body=%s", method, url, resp.StatusCode, wantStatus, strings.TrimSpace(string(responseBody)))
	}
	if out != nil {
		if err := json.Unmarshal(responseBody, out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

func signToken(userID uuid.UUID, secret string, now func() time.Time) (string, error) {
	issuedAt := now().UTC()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"user_id": userID.String(),
		"role":    "user",
		"iat":     issuedAt.Unix(),
		"exp":     issuedAt.Add(time.Hour).Unix(),
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
