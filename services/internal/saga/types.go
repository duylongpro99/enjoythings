package saga

import (
	"errors"
	"time"
)

const (
	StateStarted             = "STARTED"
	StateVerificationChecked = "VERIFICATION_CHECKED"
	StateWalletDebited       = "WALLET_DEBITED"
	StateLedgerReserved      = "LEDGER_RESERVED"
	StatePaymentProcessing   = "PAYMENT_PROCESSING"
	StateLedgerConfirmed     = "LEDGER_CONFIRMED"
	StateCompleted           = "COMPLETED"
	StateCompensatingLedger  = "COMPENSATING_LEDGER"
	StateCompensatingWallet  = "COMPENSATING_WALLET"
	StateFailed              = "FAILED"

	VerificationVerified = "verified"

	TopicPaymentExecute   = "payment.execute"
	TopicPaymentCompleted = "payment.completed"
	TopicPaymentFailed    = "payment.failed"
	TopicTxCompleted      = "tx.completed"
	TopicTxFailed         = "tx.failed"
)

var (
	ErrAlreadyExists        = errors.New("saga idempotency key already exists with different payload")
	ErrNotFound             = errors.New("saga not found")
	ErrUnverified           = errors.New("user is not verified")
	ErrVerificationNotFound = errors.New("verification not found")
)

type Saga struct {
	ID                  string
	PaymentID           string
	IdempotencyKey      string
	UserID              string
	FromWalletID        string
	ToWalletID          string
	AmountCents         int64
	Currency            string
	State               string
	LastError           string
	TraceID             string
	WalletDebitID       string
	LedgerReservationID string
	TransferID          string
	FailureCode         string
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type StartRequest struct {
	PaymentID      string
	IdempotencyKey string
	TraceID        string
	UserID         string
	FromWalletID   string
	ToWalletID     string
	AmountCents    int64
	Currency       string
}

type VerificationRequest struct {
	UserID  string
	TraceID string
}

type VerificationResult struct {
	Status string
}

type WalletDebitRequest struct {
	PaymentID      string
	IdempotencyKey string
	TraceID        string
	FromWalletID   string
	AmountCents    int64
	Currency       string
}

type WalletDebitResult struct {
	WalletDebitID string
}

type WalletCompensateRequest struct {
	PaymentID      string
	IdempotencyKey string
	TraceID        string
	FromWalletID   string
	WalletDebitID  string
	AmountCents    int64
	Currency       string
	Reason         string
}

type LedgerReserveRequest struct {
	PaymentID      string
	IdempotencyKey string
	TraceID        string
	FromWalletID   string
	ToWalletID     string
	AmountCents    int64
	Currency       string
}

type LedgerReserveResult struct {
	LedgerReservationID string
}

type LedgerConfirmRequest struct {
	PaymentID           string
	IdempotencyKey      string
	TraceID             string
	LedgerReservationID string
	WalletDebitID       string
}

type LedgerConfirmResult struct {
	TransferID  string
	CompletedAt time.Time
}

type LedgerCancelRequest struct {
	PaymentID           string
	IdempotencyKey      string
	TraceID             string
	LedgerReservationID string
	Reason              string
}

type PaymentCompleted struct {
	EventID            string    `json:"event_id"`
	PaymentID          string    `json:"payment_id"`
	IdempotencyKey     string    `json:"idempotency_key"`
	TraceID            string    `json:"trace_id"`
	ProcessorPaymentID string    `json:"processor_payment_id"`
	Status             string    `json:"status"`
	CompletedAt        time.Time `json:"completed_at"`
	OccurredAt         time.Time `json:"occurred_at"`
}

type PaymentFailed struct {
	EventID        string    `json:"event_id"`
	PaymentID      string    `json:"payment_id"`
	IdempotencyKey string    `json:"idempotency_key"`
	TraceID        string    `json:"trace_id"`
	FailureCode    string    `json:"failure_code"`
	FailureMessage string    `json:"failure_message"`
	FailedAt       time.Time `json:"failed_at"`
	OccurredAt     time.Time `json:"occurred_at"`
}

type PaymentExecute struct {
	EventID             string    `json:"event_id"`
	PaymentID           string    `json:"payment_id"`
	IdempotencyKey      string    `json:"idempotency_key"`
	TraceID             string    `json:"trace_id"`
	FromWalletID        string    `json:"from_wallet_id"`
	ToWalletID          string    `json:"to_wallet_id"`
	AmountCents         int64     `json:"amount_cents"`
	Currency            string    `json:"currency"`
	LedgerReservationID string    `json:"ledger_reservation_id"`
	WalletDebitID       string    `json:"wallet_debit_id"`
	OccurredAt          time.Time `json:"occurred_at"`
}

type TxCompleted struct {
	EventID      string    `json:"event_id"`
	PaymentID    string    `json:"payment_id"`
	TraceID      string    `json:"trace_id"`
	FromWalletID string    `json:"from_wallet_id"`
	ToWalletID   string    `json:"to_wallet_id"`
	AmountCents  int64     `json:"amount_cents"`
	Currency     string    `json:"currency"`
	TransferID   string    `json:"transfer_id"`
	CompletedAt  time.Time `json:"completed_at"`
	OccurredAt   time.Time `json:"occurred_at"`
}

type TxFailed struct {
	EventID        string    `json:"event_id"`
	PaymentID      string    `json:"payment_id"`
	TraceID        string    `json:"trace_id"`
	FromWalletID   string    `json:"from_wallet_id"`
	ToWalletID     string    `json:"to_wallet_id"`
	AmountCents    int64     `json:"amount_cents"`
	Currency       string    `json:"currency"`
	FailureCode    string    `json:"failure_code"`
	FailureMessage string    `json:"failure_message"`
	FailedAt       time.Time `json:"failed_at"`
	OccurredAt     time.Time `json:"occurred_at"`
}
