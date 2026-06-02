package domain

import "testing"

func TestWalletCanDebitOnlyPositiveAffordableAmounts(t *testing.T) {
	wallet := Wallet{Balance: 100}

	if err := wallet.Debit(0); err != ErrInvalidAmount {
		t.Fatalf("Debit(0) error = %v, want %v", err, ErrInvalidAmount)
	}
	if err := wallet.Debit(-1); err != ErrInvalidAmount {
		t.Fatalf("Debit(-1) error = %v, want %v", err, ErrInvalidAmount)
	}
	if err := wallet.Debit(101); err != ErrInsufficientFunds {
		t.Fatalf("Debit(101) error = %v, want %v", err, ErrInsufficientFunds)
	}
	if wallet.Balance != 100 {
		t.Fatalf("Balance changed after rejected debit: %d", wallet.Balance)
	}
	if err := wallet.Debit(40); err != nil {
		t.Fatalf("Debit(40) error = %v", err)
	}
	if wallet.Balance != 60 {
		t.Fatalf("Balance = %d, want 60", wallet.Balance)
	}
}

func TestWalletRejectsNegativeCredit(t *testing.T) {
	wallet := Wallet{Balance: 100}

	if err := wallet.Credit(0); err != ErrInvalidAmount {
		t.Fatalf("Credit(0) error = %v, want %v", err, ErrInvalidAmount)
	}
	if err := wallet.Credit(-1); err != ErrInvalidAmount {
		t.Fatalf("Credit(-1) error = %v, want %v", err, ErrInvalidAmount)
	}
	if wallet.Balance != 100 {
		t.Fatalf("Balance changed after rejected credit: %d", wallet.Balance)
	}
	if err := wallet.Credit(25); err != nil {
		t.Fatalf("Credit(25) error = %v", err)
	}
	if wallet.Balance != 125 {
		t.Fatalf("Balance = %d, want 125", wallet.Balance)
	}
}

func TestNormalizeCurrencyAcceptsOnlyUSD(t *testing.T) {
	for _, input := range []string{"", "USD", "usd", " UsD "} {
		t.Run(input, func(t *testing.T) {
			currency, err := NormalizeCurrency(input)
			if err != nil {
				t.Fatalf("NormalizeCurrency(%q) error = %v", input, err)
			}
			if currency != "USD" {
				t.Fatalf("currency = %q, want USD", currency)
			}
		})
	}

	if _, err := NormalizeCurrency("EUR"); err != ErrUnsupportedCurrency {
		t.Fatalf("NormalizeCurrency(EUR) error = %v, want %v", err, ErrUnsupportedCurrency)
	}
}
