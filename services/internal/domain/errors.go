package domain

import "errors"

var (
	ErrCurrencyMismatch    = errors.New("currency mismatch")
	ErrInsufficientFunds   = errors.New("insufficient funds")
	ErrInvalidAmount       = errors.New("invalid amount")
	ErrInvalidCursor       = errors.New("invalid cursor")
	ErrInvalidTransfer     = errors.New("invalid transfer")
	ErrNotFound            = errors.New("not found")
	ErrUnsupportedCurrency = errors.New("unsupported currency")
	ErrUnauthenticated     = errors.New("unauthenticated")
)
