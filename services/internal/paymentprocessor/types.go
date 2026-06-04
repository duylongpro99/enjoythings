package paymentprocessor

import (
	"errors"
	"time"
)

const (
	StatusPending   = "PENDING"
	StatusCompleted = "COMPLETED"
	StatusFailed    = "FAILED"

	PaymentExecuteTopic = "payment.execute"
)

var ErrNotFound = errors.New("payment attempt not found")

type Attempt struct {
	ID                  string
	PaymentID           string
	IdempotencyKey      string
	TraceID             string
	AmountCents         int64
	Currency            string
	LedgerReservationID string
	WalletDebitID       string
	Status              string
	AttemptCount        int
	ProcessorPaymentID  string
	FailureCode         string
	FailureMessage      string
	CompletedAt         time.Time
	FailedAt            time.Time
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type RailChargeRequest struct {
	PaymentID           string `json:"payment_id"`
	IdempotencyKey      string `json:"idempotency_key"`
	TraceID             string `json:"trace_id"`
	AmountCents         int64  `json:"amount_cents"`
	Currency            string `json:"currency"`
	LedgerReservationID string `json:"ledger_reservation_id"`
	WalletDebitID       string `json:"wallet_debit_id"`
}

type RailResult struct {
	ProcessorPaymentID string
	CompletedAt        time.Time
}

type RailFailure struct {
	Code      string
	Message   string
	Retryable bool
}

type RailError struct {
	Failure RailFailure
}

func (err RailError) Error() string {
	if err.Failure.Message != "" {
		return err.Failure.Message
	}
	if err.Failure.Code != "" {
		return err.Failure.Code
	}
	return "rail failed"
}

func RetryableRailError(code, message string) error {
	return RailError{Failure: RailFailure{Code: code, Message: message, Retryable: true}}
}

func TerminalRailError(code, message string) error {
	return RailError{Failure: RailFailure{Code: code, Message: message, Retryable: false}}
}
