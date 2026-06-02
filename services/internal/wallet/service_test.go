package wallet

import (
	"context"
	"errors"
	"testing"
	"time"

	"enjoythings/services/internal/domain"
	"enjoythings/services/internal/repo"

	"github.com/google/uuid"
)

func TestServiceCreatesWalletWithNormalizedCurrency(t *testing.T) {
	store := &fakeStore{}
	service := NewService(store)
	userID := uuid.New()

	wallet, err := service.CreateWallet(context.Background(), userID, "")
	if err != nil {
		t.Fatalf("CreateWallet: %v", err)
	}
	if wallet.UserID != userID || wallet.Currency != "USD" || wallet.Balance != 0 {
		t.Fatalf("wallet = %+v", wallet)
	}
	if store.createdCurrency != "USD" {
		t.Fatalf("created currency = %q, want USD", store.createdCurrency)
	}
}

func TestServiceRejectsUnsupportedCurrency(t *testing.T) {
	service := NewService(&fakeStore{})

	if _, err := service.CreateWallet(context.Background(), uuid.New(), "EUR"); err != domain.ErrUnsupportedCurrency {
		t.Fatalf("CreateWallet error = %v, want %v", err, domain.ErrUnsupportedCurrency)
	}
}

func TestServiceHidesWalletOwnershipMismatchAsNotFound(t *testing.T) {
	walletID := uuid.New()
	store := &fakeStore{wallets: map[uuid.UUID]domain.Wallet{
		walletID: {ID: walletID, UserID: uuid.New(), Currency: "USD"},
	}}
	service := NewService(store)

	if _, err := service.GetWallet(context.Background(), uuid.New(), walletID); err != domain.ErrNotFound {
		t.Fatalf("GetWallet error = %v, want %v", err, domain.ErrNotFound)
	}
	if _, err := service.GetBalance(context.Background(), uuid.New(), walletID); err != domain.ErrNotFound {
		t.Fatalf("GetBalance error = %v, want %v", err, domain.ErrNotFound)
	}
}

func TestServiceTransfersValidateRequestBeforeStore(t *testing.T) {
	userID := uuid.New()
	walletID := uuid.New()
	store := &fakeStore{}
	service := NewService(store)

	if _, err := service.CreateTransfer(context.Background(), userID, walletID, uuid.New(), 0); err != domain.ErrInvalidAmount {
		t.Fatalf("amount error = %v", err)
	}
	if _, err := service.CreateTransfer(context.Background(), userID, walletID, walletID, 1); err != domain.ErrInvalidTransfer {
		t.Fatalf("same wallet error = %v", err)
	}
	if store.transferCalled {
		t.Fatal("store transfer was called for invalid request")
	}
}

func TestServiceTransfersPropagateDomainErrors(t *testing.T) {
	service := NewService(&fakeStore{})

	if _, err := service.CreateTransfer(context.Background(), uuid.New(), uuid.New(), uuid.New(), 999); err != domain.ErrInsufficientFunds {
		t.Fatalf("CreateTransfer error = %v, want %v", err, domain.ErrInsufficientFunds)
	}
}

func TestServiceListsLedgerOnlyForOwnedWalletAndBoundsLimit(t *testing.T) {
	userID := uuid.New()
	walletID := uuid.New()
	cursor := repo.LedgerCursor{CreatedAt: time.Now(), ID: uuid.New(), Valid: true}
	nextCursor := repo.LedgerCursor{CreatedAt: time.Now().Add(-time.Second), ID: uuid.New(), Valid: true}
	store := &fakeStore{
		wallets: map[uuid.UUID]domain.Wallet{
			walletID: {ID: walletID, UserID: userID, Currency: "USD"},
		},
		entries: []domain.LedgerEntry{{ID: uuid.New(), WalletID: walletID, CreatedAt: time.Now()}},
		next:    nextCursor,
	}
	service := NewService(store)

	entries, next, err := service.ListLedger(context.Background(), userID, walletID, cursor, 0)
	if err != nil {
		t.Fatalf("ListLedger: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries len = %d, want 1", len(entries))
	}
	if store.listLimit != defaultLedgerLimit {
		t.Fatalf("repo list limit = %d, want %d", store.listLimit, defaultLedgerLimit)
	}
	if store.listCursor != cursor {
		t.Fatalf("repo list cursor = %s, want %s", store.listCursor, cursor)
	}
	if next != nextCursor {
		t.Fatalf("next cursor = %s, want %s", next, nextCursor)
	}

	if _, _, err := service.ListLedger(context.Background(), uuid.New(), walletID, repo.LedgerCursor{}, 50); err != domain.ErrNotFound {
		t.Fatalf("ListLedger ownership error = %v, want %v", err, domain.ErrNotFound)
	}
	if _, _, err := service.ListLedger(context.Background(), userID, walletID, repo.LedgerCursor{}, 101); err != domain.ErrInvalidAmount {
		t.Fatalf("ListLedger limit error = %v, want %v", err, domain.ErrInvalidAmount)
	}
}

type fakeStore struct {
	wallets         map[uuid.UUID]domain.Wallet
	entries         []domain.LedgerEntry
	createdCurrency string
	transferCalled  bool
	listLimit       int
	listCursor      repo.LedgerCursor
	next            repo.LedgerCursor
}

func (store *fakeStore) CreateWallet(_ context.Context, userID uuid.UUID, currency string) (domain.Wallet, error) {
	store.createdCurrency = currency
	return domain.Wallet{ID: uuid.New(), UserID: userID, Currency: currency, Balance: 0}, nil
}

func (store *fakeStore) GetWallet(_ context.Context, id uuid.UUID) (domain.Wallet, error) {
	if store.wallets == nil {
		return domain.Wallet{}, domain.ErrNotFound
	}
	wallet, ok := store.wallets[id]
	if !ok {
		return domain.Wallet{}, domain.ErrNotFound
	}
	return wallet, nil
}

func (store *fakeStore) CreateTransfer(_ context.Context, userID, fromWalletID, toWalletID uuid.UUID, amount int64) (domain.Transfer, error) {
	store.transferCalled = true
	if amount == 999 {
		return domain.Transfer{}, domain.ErrInsufficientFunds
	}
	if userID == uuid.Nil {
		return domain.Transfer{}, errors.New("unexpected nil user")
	}
	return domain.Transfer{ID: uuid.New(), FromWalletID: fromWalletID, ToWalletID: toWalletID, Amount: amount}, nil
}

func (store *fakeStore) ListLedgerEntries(_ context.Context, _ uuid.UUID, cursor repo.LedgerCursor, limit int) ([]domain.LedgerEntry, repo.LedgerCursor, error) {
	store.listLimit = limit
	store.listCursor = cursor
	return store.entries, store.next, nil
}
