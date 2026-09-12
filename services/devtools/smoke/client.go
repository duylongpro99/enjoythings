// Package smoke holds the gateway client shared by the deployed-stack validators.
package smoke

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Payment is the gateway view of a payment saga.
type Payment struct {
	PaymentID string `json:"payment_id"`
	Status    string `json:"status"`
}

type walletResponse struct {
	ID uuid.UUID `json:"id"`
}

// Client calls the gateway as one authenticated user.
type Client struct {
	http       *http.Client
	gatewayURL string
	token      string
	UserID     uuid.UUID
}

// NewClient signs a local JWT for a fresh user and targets the given gateway.
func NewClient(gatewayURL, jwtSecret string) (*Client, error) {
	if jwtSecret == "" {
		return nil, errors.New("JWT secret is required")
	}
	userID := uuid.New()
	token, err := SignToken(userID, jwtSecret)
	if err != nil {
		return nil, err
	}
	return &Client{
		http:       &http.Client{Timeout: 5 * time.Second},
		gatewayURL: strings.TrimRight(gatewayURL, "/"),
		token:      token,
		UserID:     userID,
	}, nil
}

// WaitReady blocks until the gateway reports readiness.
func (client *Client) WaitReady(ctx context.Context) error {
	return Poll(ctx, func() (bool, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, client.gatewayURL+"/readyz", nil)
		if err != nil {
			return false, err
		}
		resp, err := client.http.Do(req)
		if err != nil {
			return false, nil
		}
		defer func() { _ = resp.Body.Close() }()
		return resp.StatusCode == http.StatusOK, nil
	}, "gateway readiness")
}

// CreateWallet creates one USD wallet for the client user.
func (client *Client) CreateWallet(ctx context.Context) (uuid.UUID, error) {
	var wallet walletResponse
	if err := client.doJSON(ctx, http.MethodPost, "/v1/wallets", map[string]any{"currency": "USD"}, http.StatusCreated, &wallet); err != nil {
		return uuid.Nil, err
	}
	if wallet.ID == uuid.Nil {
		return uuid.Nil, errors.New("wallet response missing id")
	}
	return wallet.ID, nil
}

// CreateWalletPair creates a source and a destination wallet.
func (client *Client) CreateWalletPair(ctx context.Context) (uuid.UUID, uuid.UUID, error) {
	from, err := client.CreateWallet(ctx)
	if err != nil {
		return uuid.Nil, uuid.Nil, fmt.Errorf("create source wallet: %w", err)
	}
	to, err := client.CreateWallet(ctx)
	if err != nil {
		return uuid.Nil, uuid.Nil, fmt.Errorf("create destination wallet: %w", err)
	}
	return from, to, nil
}

// SubmitVerification verifies the client user through the gateway.
func (client *Client) SubmitVerification(ctx context.Context) error {
	var response struct {
		Status string `json:"status"`
	}
	if err := client.doJSON(ctx, http.MethodPost, "/v1/verification/submit", map[string]any{
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

// StartPayment posts a transfer and asserts the gateway status code.
func (client *Client) StartPayment(ctx context.Context, from, to uuid.UUID, amountCents int64, wantStatus int) (Payment, error) {
	var payment Payment
	if err := client.doJSON(ctx, http.MethodPost, "/v1/transfers", map[string]any{
		"payment_id":     uuid.NewString(),
		"from_wallet_id": from.String(),
		"to_wallet_id":   to.String(),
		"amount_cents":   amountCents,
		"currency":       "USD",
	}, wantStatus, &payment); err != nil {
		return Payment{}, err
	}
	return payment, nil
}

// GetPayment reads the current saga status of one payment.
func (client *Client) GetPayment(ctx context.Context, paymentID string) (Payment, error) {
	var payment Payment
	if err := client.doJSON(ctx, http.MethodGet, "/v1/payments/"+paymentID, nil, http.StatusOK, &payment); err != nil {
		return Payment{}, err
	}
	return payment, nil
}

// WaitPaymentState blocks until the saga reports the wanted state.
func (client *Client) WaitPaymentState(ctx context.Context, paymentID, want string) error {
	return Poll(ctx, func() (bool, error) {
		payment, err := client.GetPayment(ctx, paymentID)
		if err != nil {
			return false, err
		}
		return payment.Status == want, nil
	}, "payment "+paymentID+" state "+want)
}

func (client *Client) doJSON(ctx context.Context, method, path string, body any, wantStatus int, out any) error {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, client.gatewayURL+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", uuid.NewString())
		req.Header.Set("X-Trace-Id", uuid.NewString())
	}
	if client.token != "" {
		req.Header.Set("Authorization", "Bearer "+client.token)
	}
	resp, err := client.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != wantStatus {
		return fmt.Errorf("%s %s status=%d want=%d body=%s", method, path, resp.StatusCode, wantStatus, strings.TrimSpace(string(responseBody)))
	}
	if out != nil && len(responseBody) > 0 {
		if err := json.Unmarshal(responseBody, out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

// SetBalance seeds a wallet balance directly in the platform database.
func SetBalance(ctx context.Context, db *pgxpool.Pool, walletID uuid.UUID, balance int64) error {
	if _, err := db.Exec(ctx, `UPDATE wallets SET balance = $2, updated_at = now() WHERE id = $1`, walletID, balance); err != nil {
		return fmt.Errorf("seed source wallet balance: %w", err)
	}
	return nil
}

// Poll runs check until it succeeds or the context deadline expires.
func Poll(ctx context.Context, check func() (bool, error), description string) error {
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

// SignToken mints a local development JWT for the given user.
func SignToken(userID uuid.UUID, secret string) (string, error) {
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

// GetenvDefault reads an environment variable with a fallback.
func GetenvDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
