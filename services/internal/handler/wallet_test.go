package handler

import (
	"bytes"
	"context"
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
)

const handlerSecret = "handler-test-secret"

func TestWalletHandlerCreatesWalletFromAuthenticatedPrincipal(t *testing.T) {
	userID := uuid.New()
	walletID := uuid.New()
	service := &fakeWalletService{
		wallet: domain.Wallet{ID: walletID, UserID: userID, Currency: "USD", Balance: 0, CreatedAt: fixedTime(), UpdatedAt: fixedTime()},
	}
	handler := auth.Middleware(handlerSecret)(NewWallets(service))
	req := authedRequest(t, http.MethodPost, "/v1/wallets", bytes.NewBufferString(`{"currency":"USD"}`), userID)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body %s", rec.Code, http.StatusCreated, rec.Body.String())
	}
	if service.createUserID != userID {
		t.Fatalf("create user id = %s, want %s", service.createUserID, userID)
	}
	if got := rec.Body.String(); !bytes.Contains([]byte(got), []byte(`"balance":0`)) || !bytes.Contains([]byte(got), []byte(`"currency":"USD"`)) {
		t.Fatalf("body missing wallet fields: %s", got)
	}
}

func TestWalletHandlerMapsInvalidUUIDAndServiceErrors(t *testing.T) {
	userID := uuid.New()
	service := &fakeWalletService{err: domain.ErrNotFound}
	handler := auth.Middleware(handlerSecret)(NewWallets(service))

	req := authedRequest(t, http.MethodGet, "/v1/wallets/not-a-uuid", nil, userID)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid UUID status = %d, want %d", rec.Code, http.StatusBadRequest)
	}

	req = authedRequest(t, http.MethodGet, "/v1/wallets/"+uuid.New().String(), nil, userID)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("not found status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestTransferHandlerMapsRequestAndErrors(t *testing.T) {
	userID := uuid.New()
	fromID := uuid.New()
	toID := uuid.New()
	service := &fakeWalletService{
		transfer: domain.Transfer{ID: uuid.New(), FromWalletID: fromID, ToWalletID: toID, Amount: 1250, Status: "completed", CreatedAt: fixedTime(), FromBalance: 3750, ToBalance: 6250},
	}
	handler := auth.Middleware(handlerSecret)(NewTransfers(service))
	req := authedRequest(t, http.MethodPost, "/v1/transfers", bytes.NewBufferString(`{"from_wallet_id":"`+fromID.String()+`","to_wallet_id":"`+toID.String()+`","amount":1250}`), userID)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d; body %s", rec.Code, http.StatusCreated, rec.Body.String())
	}
	if service.transferAmount != 1250 {
		t.Fatalf("transfer amount = %d, want 1250", service.transferAmount)
	}

	service.err = domain.ErrInsufficientFunds
	req = authedRequest(t, http.MethodPost, "/v1/transfers", bytes.NewBufferString(`{"from_wallet_id":"`+fromID.String()+`","to_wallet_id":"`+toID.String()+`","amount":1250}`), userID)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("insufficient funds status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}
}

func TestLedgerHandlerValidatesLimitAndReturnsEntries(t *testing.T) {
	userID := uuid.New()
	walletID := uuid.New()
	service := &fakeWalletService{
		entries: []domain.LedgerEntry{{ID: uuid.New(), WalletID: walletID, TransferID: uuid.New(), Direction: "debit", Amount: 100, BalanceAfter: 900, CreatedAt: fixedTime()}},
	}
	handler := auth.Middleware(handlerSecret)(NewLedger(service))

	req := authedRequest(t, http.MethodGet, "/v1/ledger/"+walletID.String()+"?limit=1", nil, userID)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if service.listLimit != 1 {
		t.Fatalf("list limit = %d, want 1", service.listLimit)
	}

	req = authedRequest(t, http.MethodGet, "/v1/ledger/"+walletID.String()+"?limit=101", nil, userID)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid limit status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

type fakeWalletService struct {
	wallet         domain.Wallet
	transfer       domain.Transfer
	entries        []domain.LedgerEntry
	err            error
	createUserID   uuid.UUID
	transferAmount int64
	listLimit      int
}

func (service *fakeWalletService) CreateWallet(_ context.Context, userID uuid.UUID, currency string) (domain.Wallet, error) {
	service.createUserID = userID
	if service.err != nil {
		return domain.Wallet{}, service.err
	}
	if service.wallet.ID == uuid.Nil {
		service.wallet = domain.Wallet{ID: uuid.New(), UserID: userID, Currency: currency, CreatedAt: fixedTime(), UpdatedAt: fixedTime()}
	}
	return service.wallet, nil
}

func (service *fakeWalletService) GetWallet(context.Context, uuid.UUID, uuid.UUID) (domain.Wallet, error) {
	if service.err != nil {
		return domain.Wallet{}, service.err
	}
	return service.wallet, nil
}

func (service *fakeWalletService) GetBalance(context.Context, uuid.UUID, uuid.UUID) (domain.Wallet, error) {
	return service.GetWallet(context.Background(), uuid.Nil, uuid.Nil)
}

func (service *fakeWalletService) CreateTransfer(_ context.Context, _ uuid.UUID, _ uuid.UUID, _ uuid.UUID, amount int64) (domain.Transfer, error) {
	service.transferAmount = amount
	if service.err != nil {
		return domain.Transfer{}, service.err
	}
	return service.transfer, nil
}

func (service *fakeWalletService) ListLedger(_ context.Context, _ uuid.UUID, _ uuid.UUID, _ repo.LedgerCursor, limit int) ([]domain.LedgerEntry, repo.LedgerCursor, error) {
	service.listLimit = limit
	if service.err != nil {
		return nil, repo.LedgerCursor{}, service.err
	}
	return service.entries, repo.LedgerCursor{}, nil
}

func authedRequest(t *testing.T, method, target string, body io.Reader, userID uuid.UUID) *http.Request {
	t.Helper()

	req := httptest.NewRequest(method, target, body)
	req.Header.Set("Authorization", "Bearer "+handlerToken(t, userID))
	return req
}

func handlerToken(t *testing.T, userID uuid.UUID) string {
	t.Helper()

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"user_id": userID.String(),
		"role":    "user",
		"exp":     time.Now().Add(time.Hour).Unix(),
	})
	signed, err := token.SignedString([]byte(handlerSecret))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return signed
}

func fixedTime() time.Time {
	return time.Date(2026, 6, 2, 0, 0, 0, 0, time.UTC)
}
