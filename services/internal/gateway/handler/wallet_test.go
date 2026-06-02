package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"enjoythings/services/internal/auth"
	"enjoythings/services/internal/domain"
	"enjoythings/services/internal/repo"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const testSecret = "gateway-handler-secret"

func TestInvalidJWTShortCircuitsBeforeWalletClient(t *testing.T) {
	client := &fakeWalletClient{}
	handler := auth.Middleware(testSecret)(NewWallets(client))
	req := httptest.NewRequest(http.MethodPost, "/v1/wallets", bytes.NewBufferString(`{"currency":"USD"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer invalid")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d; body %s", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}
	if client.called {
		t.Fatal("wallet client was called")
	}
}

func TestWalletRoutesPreservePhase1ResponseShapes(t *testing.T) {
	userID := uuid.New()
	walletID := uuid.New()
	createdAt := time.Date(2026, 6, 2, 1, 2, 3, 0, time.UTC)
	client := &fakeWalletClient{
		wallet: domain.Wallet{
			ID:        walletID,
			UserID:    userID,
			Balance:   1500,
			Currency:  "USD",
			CreatedAt: createdAt,
			UpdatedAt: createdAt.Add(time.Minute),
		},
	}
	handler := auth.Middleware(testSecret)(NewWallets(client))

	req := authedRequest(t, http.MethodGet, "/v1/wallets/"+walletID.String(), nil, userID)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("wallet status = %d, want %d; body %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var walletResponse struct {
		ID        uuid.UUID `json:"id"`
		UserID    uuid.UUID `json:"user_id"`
		Balance   int64     `json:"balance"`
		Currency  string    `json:"currency"`
		CreatedAt time.Time `json:"created_at"`
		UpdatedAt time.Time `json:"updated_at"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&walletResponse); err != nil {
		t.Fatalf("decode wallet response: %v", err)
	}
	if walletResponse.ID != walletID || walletResponse.UserID != userID || walletResponse.Balance != 1500 || walletResponse.Currency != "USD" || !walletResponse.CreatedAt.Equal(createdAt) {
		t.Fatalf("wallet response mismatch: %+v", walletResponse)
	}

	req = authedRequest(t, http.MethodGet, "/v1/wallets/"+walletID.String()+"/balance", nil, userID)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("balance status = %d, want %d; body %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var balanceResponse struct {
		WalletID uuid.UUID `json:"wallet_id"`
		Balance  int64     `json:"balance"`
		Currency string    `json:"currency"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&balanceResponse); err != nil {
		t.Fatalf("decode balance response: %v", err)
	}
	if balanceResponse.WalletID != walletID || balanceResponse.Balance != 1500 || balanceResponse.Currency != "USD" {
		t.Fatalf("balance response mismatch: %+v", balanceResponse)
	}
}

func TestGatewayMapsGRPCStatusToHTTPEnvelope(t *testing.T) {
	userID := uuid.New()
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{name: "invalid argument", err: status.Error(codes.InvalidArgument, "request body is invalid"), status: http.StatusBadRequest, code: "invalid_request"},
		{name: "not found", err: status.Error(codes.NotFound, "wallet not found"), status: http.StatusNotFound, code: "wallet_not_found"},
		{name: "failed precondition", err: status.Error(codes.FailedPrecondition, "insufficient funds"), status: http.StatusUnprocessableEntity, code: "insufficient_funds"},
		{name: "internal", err: status.Error(codes.Internal, "internal server error"), status: http.StatusInternalServerError, code: "internal_error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := &fakeWalletClient{err: tt.err}
			handler := auth.Middleware(testSecret)(NewWallets(client))
			req := authedRequest(t, http.MethodGet, "/v1/wallets/"+uuid.NewString(), nil, userID)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != tt.status {
				t.Fatalf("status = %d, want %d; body %s", rec.Code, tt.status, rec.Body.String())
			}
			var response struct {
				Error struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if response.Error.Code != tt.code || response.Error.Message == "" {
				t.Fatalf("error response = %+v, want code %q and message", response.Error, tt.code)
			}
		})
	}
}

func TestTransferRouteMapsInsufficientFundsTo422(t *testing.T) {
	userID := uuid.New()
	fromID := uuid.New()
	toID := uuid.New()
	client := &fakeWalletClient{err: status.Error(codes.FailedPrecondition, "insufficient funds")}
	handler := auth.Middleware(testSecret)(NewTransfers(client))
	req := authedRequest(t, http.MethodPost, "/v1/transfers", bytes.NewBufferString(`{"from_wallet_id":"`+fromID.String()+`","to_wallet_id":"`+toID.String()+`","amount":1250}`), userID)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d; body %s", rec.Code, http.StatusUnprocessableEntity, rec.Body.String())
	}
	var response struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Error.Code != "insufficient_funds" {
		t.Fatalf("error code = %q, want insufficient_funds", response.Error.Code)
	}
}

func TestLedgerRoutePreservesPhase1ShapeAndMapsGRPCErrors(t *testing.T) {
	userID := uuid.New()
	walletID := uuid.New()
	entryID := uuid.New()
	transferID := uuid.New()
	createdAt := time.Date(2026, 6, 2, 8, 0, 0, 0, time.UTC)
	next := repo.LedgerCursor{CreatedAt: createdAt, ID: entryID, Valid: true}
	client := &fakeWalletClient{
		entries: []domain.LedgerEntry{{
			ID:           entryID,
			WalletID:     walletID,
			TransferID:   transferID,
			Direction:    "debit",
			Amount:       500,
			BalanceAfter: 1500,
			CreatedAt:    createdAt,
		}},
		next: next,
	}
	handler := auth.Middleware(testSecret)(NewLedger(client))
	req := authedRequest(t, http.MethodGet, "/v1/ledger/"+walletID.String()+"?limit=1", nil, userID)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if client.limit != 1 {
		t.Fatalf("limit = %d, want 1", client.limit)
	}
	var response struct {
		WalletID uuid.UUID `json:"wallet_id"`
		Entries  []struct {
			ID           uuid.UUID `json:"id"`
			TransferID   uuid.UUID `json:"transfer_id"`
			Direction    string    `json:"direction"`
			Amount       int64     `json:"amount"`
			BalanceAfter int64     `json:"balance_after"`
			CreatedAt    time.Time `json:"created_at"`
		} `json:"entries"`
		NextCursor *string `json:"next_cursor"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode ledger response: %v", err)
	}
	if response.WalletID != walletID || len(response.Entries) != 1 || response.Entries[0].ID != entryID || response.Entries[0].TransferID != transferID || response.Entries[0].Direction != "debit" || response.Entries[0].Amount != 500 || response.Entries[0].BalanceAfter != 1500 || !response.Entries[0].CreatedAt.Equal(createdAt) || response.NextCursor == nil {
		t.Fatalf("ledger response mismatch: %+v", response)
	}
	decoded, err := decodeLedgerCursor(*response.NextCursor)
	if err != nil {
		t.Fatalf("decode next cursor: %v", err)
	}
	if decoded != next {
		t.Fatalf("next cursor = %s, want %s", decoded, next)
	}

	client.err = status.Error(codes.InvalidArgument, "limit is invalid")
	req = authedRequest(t, http.MethodGet, "/v1/ledger/"+walletID.String(), nil, userID)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid argument status = %d, want %d; body %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

type fakeWalletClient struct {
	wallet   domain.Wallet
	transfer domain.Transfer
	entries  []domain.LedgerEntry
	next     repo.LedgerCursor
	err      error
	called   bool
	limit    int
	cursor   repo.LedgerCursor
}

func (client *fakeWalletClient) CreateWallet(context.Context, uuid.UUID, string) (domain.Wallet, error) {
	client.called = true
	if client.err != nil {
		return domain.Wallet{}, client.err
	}
	return client.wallet, nil
}

func (client *fakeWalletClient) GetWallet(context.Context, uuid.UUID, uuid.UUID) (domain.Wallet, error) {
	client.called = true
	if client.err != nil {
		return domain.Wallet{}, client.err
	}
	return client.wallet, nil
}

func (client *fakeWalletClient) GetBalance(context.Context, uuid.UUID, uuid.UUID) (domain.Wallet, error) {
	return client.GetWallet(context.Background(), uuid.Nil, uuid.Nil)
}

func (client *fakeWalletClient) CreateTransfer(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, int64) (domain.Transfer, error) {
	client.called = true
	if client.err != nil {
		return domain.Transfer{}, client.err
	}
	return client.transfer, nil
}

func (client *fakeWalletClient) ListLedger(_ context.Context, _ uuid.UUID, _ uuid.UUID, cursor repo.LedgerCursor, limit int) ([]domain.LedgerEntry, repo.LedgerCursor, error) {
	client.called = true
	client.cursor = cursor
	client.limit = limit
	if client.err != nil {
		return nil, repo.LedgerCursor{}, client.err
	}
	return client.entries, client.next, nil
}

func authedRequest(t *testing.T, method, target string, body io.Reader, userID uuid.UUID) *http.Request {
	t.Helper()

	req := httptest.NewRequest(method, target, body)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"user_id": userID.String(),
		"role":    "user",
		"exp":     time.Now().Add(time.Hour).Unix(),
	})
	signed, err := token.SignedString([]byte(testSecret))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+signed)
	return req
}

var _ = errors.Is
