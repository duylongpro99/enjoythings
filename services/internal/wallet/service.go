package wallet

import (
	"context"

	"enjoythings/services/internal/domain"
	"enjoythings/services/internal/repo"

	"github.com/google/uuid"
)

const (
	defaultLedgerLimit = 50
	maxLedgerLimit     = 100
)

type Store interface {
	CreateWallet(context.Context, uuid.UUID, string) (domain.Wallet, error)
	GetWallet(context.Context, uuid.UUID) (domain.Wallet, error)
	CreateTransfer(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, int64) (domain.Transfer, error)
	ListLedgerEntries(context.Context, uuid.UUID, repo.LedgerCursor, int) ([]domain.LedgerEntry, repo.LedgerCursor, error)
}

type Service struct {
	store Store
}

func NewService(store Store) *Service {
	return &Service{store: store}
}

func (service *Service) CreateWallet(ctx context.Context, userID uuid.UUID, currency string) (domain.Wallet, error) {
	normalized, err := domain.NormalizeCurrency(currency)
	if err != nil {
		return domain.Wallet{}, err
	}
	return service.store.CreateWallet(ctx, userID, normalized)
}

func (service *Service) GetWallet(ctx context.Context, userID, walletID uuid.UUID) (domain.Wallet, error) {
	wallet, err := service.store.GetWallet(ctx, walletID)
	if err != nil {
		return domain.Wallet{}, err
	}
	if wallet.UserID != userID {
		return domain.Wallet{}, domain.ErrNotFound
	}
	return wallet, nil
}

func (service *Service) GetBalance(ctx context.Context, userID, walletID uuid.UUID) (domain.Wallet, error) {
	return service.GetWallet(ctx, userID, walletID)
}

func (service *Service) CreateTransfer(ctx context.Context, userID, fromWalletID, toWalletID uuid.UUID, amount int64) (domain.Transfer, error) {
	if amount <= 0 {
		return domain.Transfer{}, domain.ErrInvalidAmount
	}
	if fromWalletID == toWalletID {
		return domain.Transfer{}, domain.ErrInvalidTransfer
	}
	return service.store.CreateTransfer(ctx, userID, fromWalletID, toWalletID, amount)
}

func (service *Service) ListLedger(ctx context.Context, userID, walletID uuid.UUID, cursor repo.LedgerCursor, limit int) ([]domain.LedgerEntry, repo.LedgerCursor, error) {
	if limit == 0 {
		limit = defaultLedgerLimit
	}
	if limit < 1 || limit > maxLedgerLimit {
		return nil, repo.LedgerCursor{}, domain.ErrInvalidAmount
	}
	wallet, err := service.GetWallet(ctx, userID, walletID)
	if err != nil {
		return nil, repo.LedgerCursor{}, err
	}
	entries, next, err := service.store.ListLedgerEntries(ctx, wallet.ID, cursor, limit)
	if err != nil {
		return nil, repo.LedgerCursor{}, err
	}
	return entries, next, nil
}
