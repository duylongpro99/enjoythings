package service

import (
	"bytes"
	"context"
	"encoding/json"
	"enjoythings/services/internal/auth"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"enjoythings/services/internal/repo"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestRouterFullWalletAndTransferFlowAgainstPostgres(t *testing.T) {
	ctx := context.Background()
	db := newRouterIntegrationDB(t, ctx)
	userID := uuid.New()
	otherUserID := uuid.New()
	router := NewRouter(db, db, auth.HMACVerifier("integration-secret"))

	fromWalletID := createWalletThroughRouter(t, router, userID)
	toWalletID := createWalletThroughRouter(t, router, otherUserID)
	if err := db.SetWalletBalanceForTest(ctx, fromWalletID, 1000); err != nil {
		t.Fatalf("fund source wallet: %v", err)
	}

	body := bytes.NewBufferString(fmt.Sprintf(
		`{"from_wallet_id":"%s","to_wallet_id":"%s","amount":250}`,
		fromWalletID,
		toWalletID,
	))
	req := httptest.NewRequest(http.MethodPost, "/v1/transfers", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+integrationToken(t, userID))
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("transfer status = %d, want %d; body %s", rec.Code, http.StatusCreated, rec.Body.String())
	}
	var transferResponse struct {
		ID uuid.UUID `json:"id"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&transferResponse); err != nil {
		t.Fatalf("decode transfer response: %v", err)
	}
	if err := db.AppendTransferEntries(ctx, repo.LedgerTransfer{
		TransferID:   transferResponse.ID,
		FromWalletID: fromWalletID,
		ToWalletID:   toWalletID,
		AmountCents:  250,
		Currency:     "USD",
	}); err != nil {
		t.Fatalf("append ledger entries: %v", err)
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/wallets/"+fromWalletID.String()+"/balance", nil)
	req.Header.Set("Authorization", "Bearer "+integrationToken(t, userID))
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("balance status = %d, want %d; body %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var balanceResponse struct {
		Balance int64 `json:"balance"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&balanceResponse); err != nil {
		t.Fatalf("decode balance response: %v", err)
	}
	if balanceResponse.Balance != 750 {
		t.Fatalf("balance = %d, want 750", balanceResponse.Balance)
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/ledger/"+fromWalletID.String()+"?limit=1", nil)
	req.Header.Set("Authorization", "Bearer "+integrationToken(t, userID))
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("ledger status = %d, want %d; body %s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var ledgerResponse struct {
		WalletID uuid.UUID `json:"wallet_id"`
		Entries  []struct {
			Direction    string    `json:"direction"`
			Amount       int64     `json:"amount"`
			BalanceAfter int64     `json:"balance_after"`
			TransferID   uuid.UUID `json:"transfer_id"`
		} `json:"entries"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&ledgerResponse); err != nil {
		t.Fatalf("decode ledger response: %v", err)
	}
	if ledgerResponse.WalletID != fromWalletID || len(ledgerResponse.Entries) != 1 || ledgerResponse.Entries[0].Direction != "debit" || ledgerResponse.Entries[0].Amount != 250 || ledgerResponse.Entries[0].BalanceAfter != 750 {
		t.Fatalf("ledger response mismatch: %+v", ledgerResponse)
	}

	req = httptest.NewRequest(http.MethodGet, "/v1/ledger/"+fromWalletID.String(), nil)
	req.Header.Set("Authorization", "Bearer "+integrationToken(t, otherUserID))
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("other user ledger status = %d, want %d; body %s", rec.Code, http.StatusNotFound, rec.Body.String())
	}
}

func newRouterIntegrationDB(t *testing.T, ctx context.Context) *repo.Database {
	t.Helper()

	req := testcontainers.ContainerRequest{
		Image:        "postgres:16-alpine",
		ExposedPorts: []string{"5432/tcp"},
		Env: map[string]string{
			"POSTGRES_DB":       "enjoythings_test",
			"POSTGRES_USER":     "enjoythings",
			"POSTGRES_PASSWORD": "enjoythings_test_password",
		},
		WaitingFor: wait.ForListeningPort("5432/tcp").WithStartupTimeout(60 * time.Second),
	}
	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Skipf("Postgres container unavailable: %v", err)
	}
	t.Cleanup(func() {
		if err := container.Terminate(context.Background()); err != nil {
			t.Logf("terminate Postgres container: %v", err)
		}
	})

	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("container host: %v", err)
	}
	port, err := container.MappedPort(ctx, "5432/tcp")
	if err != nil {
		t.Fatalf("container port: %v", err)
	}
	databaseURL := fmt.Sprintf(
		"postgres://enjoythings:enjoythings_test_password@%s:%s/enjoythings_test?sslmode=disable",
		host,
		port.Port(),
	)
	db, err := repo.Connect(ctx, databaseURL, 4)
	if err != nil {
		t.Fatalf("connect to Postgres container: %v", err)
	}
	t.Cleanup(db.Close)
	if err := db.RunMigrations(ctx); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	return db
}

func createWalletThroughRouter(t *testing.T, router http.Handler, userID uuid.UUID) uuid.UUID {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, "/v1/wallets", bytes.NewBufferString(`{"currency":"USD"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+integrationToken(t, userID))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create wallet status = %d, want %d; body %s", rec.Code, http.StatusCreated, rec.Body.String())
	}
	var response struct {
		ID uuid.UUID `json:"id"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&response); err != nil {
		t.Fatalf("decode create wallet response: %v", err)
	}
	return response.ID
}

func integrationToken(t *testing.T, userID uuid.UUID) string {
	t.Helper()

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"user_id": userID.String(),
		"role":    "user",
		"exp":     time.Now().Add(time.Hour).Unix(),
	})
	signed, err := token.SignedString([]byte("integration-secret"))
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return signed
}
