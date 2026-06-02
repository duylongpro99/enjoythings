package handler

import (
	"bytes"
	"context"
	"encoding/json"
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

func TestWalletHandlerRequiresJSONContentTypeForCreate(t *testing.T) {
	userID := uuid.New()
	service := &fakeWalletService{}
	handler := auth.Middleware(handlerSecret)(NewWallets(service))
	req := authedRequest(t, http.MethodPost, "/v1/wallets", bytes.NewBufferString(`{"currency":"USD"}`), userID)
	req.Header.Del("Content-Type")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	want := "{\"error\":{\"code\":\"invalid_request\",\"message\":\"content type must be application/json\"}}\n"
	if got := rec.Body.String(); got != want {
		t.Fatalf("body = %q, want %q", got, want)
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

func TestWalletHandlerReturnsWalletAndBalanceResponseShapes(t *testing.T) {
	userID := uuid.New()
	walletID := uuid.New()
	service := &fakeWalletService{
		wallet: domain.Wallet{ID: walletID, UserID: userID, Currency: "USD", Balance: 1500, CreatedAt: fixedTime(), UpdatedAt: fixedTime()},
	}
	handler := auth.Middleware(handlerSecret)(NewWallets(service))

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
	if walletResponse.ID != walletID || walletResponse.UserID != userID || walletResponse.Balance != 1500 || walletResponse.Currency != "USD" || !walletResponse.CreatedAt.Equal(fixedTime()) || !walletResponse.UpdatedAt.Equal(fixedTime()) {
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

func TestWalletHandlerMethodMismatchUsesErrorEnvelope(t *testing.T) {
	userID := uuid.New()
	service := &fakeWalletService{}
	handler := auth.Middleware(handlerSecret)(NewWallets(service))
	req := authedRequest(t, http.MethodPut, "/v1/wallets/"+uuid.New().String(), nil, userID)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	want := "{\"error\":{\"code\":\"not_found\",\"message\":\"resource not found\"}}\n"
	if got := rec.Body.String(); got != want {
		t.Fatalf("body = %q, want %q", got, want)
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
	var transferResponse struct {
		ID           uuid.UUID `json:"id"`
		FromWalletID uuid.UUID `json:"from_wallet_id"`
		ToWalletID   uuid.UUID `json:"to_wallet_id"`
		Amount       int64     `json:"amount"`
		Status       string    `json:"status"`
		CreatedAt    time.Time `json:"created_at"`
		Balances     struct {
			From int64 `json:"from"`
			To   int64 `json:"to"`
		} `json:"balances"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&transferResponse); err != nil {
		t.Fatalf("decode transfer response: %v", err)
	}
	if transferResponse.FromWalletID != fromID || transferResponse.ToWalletID != toID || transferResponse.Amount != 1250 || transferResponse.Status != "completed" || transferResponse.Balances.From != 3750 || transferResponse.Balances.To != 6250 {
		t.Fatalf("transfer response mismatch: %+v", transferResponse)
	}

	service.err = domain.ErrInsufficientFunds
	req = authedRequest(t, http.MethodPost, "/v1/transfers", bytes.NewBufferString(`{"from_wallet_id":"`+fromID.String()+`","to_wallet_id":"`+toID.String()+`","amount":1250}`), userID)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("insufficient funds status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}
}

func TestTransferHandlerRequiresJSONContentType(t *testing.T) {
	userID := uuid.New()
	service := &fakeWalletService{}
	handler := auth.Middleware(handlerSecret)(NewTransfers(service))
	req := authedRequest(t, http.MethodPost, "/v1/transfers", bytes.NewBufferString(`{"amount":1250}`), userID)
	req.Header.Del("Content-Type")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	want := "{\"error\":{\"code\":\"invalid_request\",\"message\":\"content type must be application/json\"}}\n"
	if got := rec.Body.String(); got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func TestTransferHandlerValidatesRequestShape(t *testing.T) {
	userID := uuid.New()
	fromID := uuid.New()
	toID := uuid.New()
	tests := []struct {
		name   string
		body   string
		status int
		code   string
	}{
		{
			name:   "invalid from wallet id",
			body:   `{"from_wallet_id":"not-a-uuid","to_wallet_id":"` + toID.String() + `","amount":1}`,
			status: http.StatusBadRequest,
			code:   "invalid_request",
		},
		{
			name:   "invalid to wallet id",
			body:   `{"from_wallet_id":"` + fromID.String() + `","to_wallet_id":"not-a-uuid","amount":1}`,
			status: http.StatusBadRequest,
			code:   "invalid_request",
		},
		{
			name:   "non-positive amount",
			body:   `{"from_wallet_id":"` + fromID.String() + `","to_wallet_id":"` + toID.String() + `","amount":0}`,
			status: http.StatusUnprocessableEntity,
			code:   "invalid_amount",
		},
		{
			name:   "same wallet",
			body:   `{"from_wallet_id":"` + fromID.String() + `","to_wallet_id":"` + fromID.String() + `","amount":1}`,
			status: http.StatusUnprocessableEntity,
			code:   "invalid_transfer",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeWalletService{}
			handler := auth.Middleware(handlerSecret)(NewTransfers(service))
			req := authedRequest(t, http.MethodPost, "/v1/transfers", bytes.NewBufferString(tt.body), userID)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != tt.status {
				t.Fatalf("status = %d, want %d; body %s", rec.Code, tt.status, rec.Body.String())
			}
			var response struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
				t.Fatalf("decode error response: %v", err)
			}
			if response.Error.Code != tt.code {
				t.Fatalf("error code = %q, want %q", response.Error.Code, tt.code)
			}
		})
	}
}

func TestTransferHandlerMapsServiceDomainErrors(t *testing.T) {
	userID := uuid.New()
	fromID := uuid.New()
	toID := uuid.New()
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{name: "source or destination wallet not found", err: domain.ErrNotFound, status: http.StatusNotFound, code: "wallet_not_found"},
		{name: "currency mismatch", err: domain.ErrCurrencyMismatch, status: http.StatusUnprocessableEntity, code: "currency_mismatch"},
		{name: "insufficient funds", err: domain.ErrInsufficientFunds, status: http.StatusUnprocessableEntity, code: "insufficient_funds"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := &fakeWalletService{err: tt.err}
			handler := auth.Middleware(handlerSecret)(NewTransfers(service))
			req := authedRequest(t, http.MethodPost, "/v1/transfers", bytes.NewBufferString(`{"from_wallet_id":"`+fromID.String()+`","to_wallet_id":"`+toID.String()+`","amount":1250}`), userID)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != tt.status {
				t.Fatalf("status = %d, want %d; body %s", rec.Code, tt.status, rec.Body.String())
			}
			var response struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
				t.Fatalf("decode error response: %v", err)
			}
			if response.Error.Code != tt.code {
				t.Fatalf("error code = %q, want %q", response.Error.Code, tt.code)
			}
		})
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
	var ledgerResponse struct {
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
	if err := json.NewDecoder(rec.Body).Decode(&ledgerResponse); err != nil {
		t.Fatalf("decode ledger response: %v", err)
	}
	if ledgerResponse.WalletID != walletID || len(ledgerResponse.Entries) != 1 || ledgerResponse.Entries[0].Direction != "debit" || ledgerResponse.Entries[0].Amount != 100 || ledgerResponse.Entries[0].BalanceAfter != 900 || ledgerResponse.NextCursor != nil {
		t.Fatalf("ledger response mismatch: %+v", ledgerResponse)
	}

	req = authedRequest(t, http.MethodGet, "/v1/ledger/"+walletID.String()+"?limit=101", nil, userID)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid limit status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestLedgerHandlerValidatesWalletIDAndCursor(t *testing.T) {
	userID := uuid.New()
	service := &fakeWalletService{}
	handler := auth.Middleware(handlerSecret)(NewLedger(service))

	req := authedRequest(t, http.MethodGet, "/v1/ledger/not-a-uuid", nil, userID)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid wallet id status = %d, want %d; body %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	assertErrorCode(t, rec.Body, "invalid_request")

	req = authedRequest(t, http.MethodGet, "/v1/ledger/"+uuid.New().String()+"?cursor=not-base64url", nil, userID)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid cursor status = %d, want %d; body %s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	assertErrorCode(t, rec.Body, "invalid_cursor")
}

func TestLedgerHandlerPassesDecodedCursorAndReturnsNextCursor(t *testing.T) {
	userID := uuid.New()
	walletID := uuid.New()
	next := repo.LedgerCursor{CreatedAt: fixedTime(), ID: uuid.New(), Valid: true}
	cursor := repo.LedgerCursor{CreatedAt: fixedTime().Add(time.Second), ID: uuid.New(), Valid: true}
	service := &fakeWalletService{
		entries: []domain.LedgerEntry{{ID: uuid.New(), WalletID: walletID, TransferID: uuid.New(), Direction: "credit", Amount: 250, BalanceAfter: 1250, CreatedAt: fixedTime()}},
		next:    next,
	}
	handler := auth.Middleware(handlerSecret)(NewLedger(service))

	req := authedRequest(t, http.MethodGet, "/v1/ledger/"+walletID.String()+"?cursor="+encodeLedgerCursor(cursor), nil, userID)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if service.listCursor != cursor {
		t.Fatalf("decoded cursor = %s, want %s", service.listCursor, cursor)
	}
	var response struct {
		NextCursor *string `json:"next_cursor"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode ledger response: %v", err)
	}
	if response.NextCursor == nil {
		t.Fatal("next_cursor missing")
	}
	decoded, err := decodeLedgerCursor(*response.NextCursor)
	if err != nil {
		t.Fatalf("decode next cursor: %v", err)
	}
	if decoded != next {
		t.Fatalf("next cursor = %s, want %s", decoded, next)
	}
}

type fakeWalletService struct {
	wallet         domain.Wallet
	transfer       domain.Transfer
	entries        []domain.LedgerEntry
	next           repo.LedgerCursor
	err            error
	createUserID   uuid.UUID
	transferAmount int64
	listLimit      int
	listCursor     repo.LedgerCursor
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

func (service *fakeWalletService) ListLedger(_ context.Context, _ uuid.UUID, _ uuid.UUID, cursor repo.LedgerCursor, limit int) ([]domain.LedgerEntry, repo.LedgerCursor, error) {
	service.listLimit = limit
	service.listCursor = cursor
	if service.err != nil {
		return nil, repo.LedgerCursor{}, service.err
	}
	return service.entries, service.next, nil
}

func assertErrorCode(t *testing.T, body *bytes.Buffer, want string) {
	t.Helper()

	var response struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(body).Decode(&response); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if response.Error.Code != want {
		t.Fatalf("error code = %q, want %q", response.Error.Code, want)
	}
}

func authedRequest(t *testing.T, method, target string, body io.Reader, userID uuid.UUID) *http.Request {
	t.Helper()

	req := httptest.NewRequest(method, target, body)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
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
