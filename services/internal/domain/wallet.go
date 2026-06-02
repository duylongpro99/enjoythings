package domain

import (
	"strings"
	"time"

	"github.com/google/uuid"
)

const DefaultCurrency = "USD"

type Wallet struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	Balance   int64
	Currency  string
	CreatedAt time.Time
	UpdatedAt time.Time
}

func NormalizeCurrency(currency string) (string, error) {
	normalized := strings.ToUpper(strings.TrimSpace(currency))
	if normalized == "" {
		normalized = DefaultCurrency
	}
	if normalized != DefaultCurrency {
		return "", ErrUnsupportedCurrency
	}
	return normalized, nil
}

func (wallet *Wallet) CanDebit(amount int64) bool {
	return amount > 0 && wallet.Balance >= amount
}

func (wallet *Wallet) Debit(amount int64) error {
	if amount <= 0 {
		return ErrInvalidAmount
	}
	if wallet.Balance < amount {
		return ErrInsufficientFunds
	}
	wallet.Balance -= amount
	return nil
}

func (wallet *Wallet) Credit(amount int64) error {
	if amount <= 0 {
		return ErrInvalidAmount
	}
	wallet.Balance += amount
	return nil
}

type Transfer struct {
	ID           uuid.UUID
	FromWalletID uuid.UUID
	ToWalletID   uuid.UUID
	Amount       int64
	Status       string
	CreatedAt    time.Time
	FromBalance  int64
	ToBalance    int64
}

type LedgerEntry struct {
	ID           uuid.UUID
	WalletID     uuid.UUID
	TransferID   uuid.UUID
	Direction    string
	Amount       int64
	BalanceAfter int64
	CreatedAt    time.Time
}
