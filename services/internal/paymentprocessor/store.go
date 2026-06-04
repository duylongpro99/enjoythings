package paymentprocessor

import (
	"context"
	"errors"
	"time"

	"enjoythings/services/internal/saga"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type pgDB interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

type PostgresStore struct {
	db pgDB
}

func NewPostgresStore(db pgDB) *PostgresStore {
	return &PostgresStore{db: db}
}

func (store *PostgresStore) GetByPaymentID(ctx context.Context, paymentID string) (Attempt, error) {
	return scanAttempt(store.db.QueryRow(ctx, paymentAttemptSelectSQL+` WHERE payment_id = $1`, paymentID))
}

func (store *PostgresStore) CreatePending(ctx context.Context, command saga.PaymentExecute, now time.Time) (Attempt, error) {
	row := store.db.QueryRow(ctx, `
INSERT INTO payment_attempts (
  payment_id, idempotency_key, trace_id, amount_cents, currency,
  ledger_reservation_id, wallet_debit_id, status, created_at, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, 'PENDING',
  COALESCE(NULLIF($8::timestamptz, '0001-01-01 00:00:00+00'::timestamptz), now()),
  COALESCE(NULLIF($9::timestamptz, '0001-01-01 00:00:00+00'::timestamptz), now())
)
ON CONFLICT (payment_id) DO NOTHING
RETURNING id::text, payment_id, idempotency_key, trace_id, amount_cents, currency,
  ledger_reservation_id, wallet_debit_id, status, attempt_count, processor_payment_id,
  failure_code, failure_message, completed_at, failed_at, created_at, updated_at`,
		command.PaymentID,
		command.IdempotencyKey,
		command.TraceID,
		command.AmountCents,
		command.Currency,
		command.LedgerReservationID,
		command.WalletDebitID,
		now,
		now,
	)
	attempt, err := scanAttempt(row)
	if err == nil {
		return attempt, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Attempt{}, err
	}
	return store.GetByPaymentID(ctx, command.PaymentID)
}

func (store *PostgresStore) MarkAttemptStarted(ctx context.Context, paymentID string, attemptCount int, now time.Time) (Attempt, error) {
	return scanAttempt(store.db.QueryRow(ctx, `
UPDATE payment_attempts
SET attempt_count = $2,
  updated_at = COALESCE(NULLIF($3::timestamptz, '0001-01-01 00:00:00+00'::timestamptz), now())
WHERE payment_id = $1
RETURNING id::text, payment_id, idempotency_key, trace_id, amount_cents, currency,
  ledger_reservation_id, wallet_debit_id, status, attempt_count, processor_payment_id,
  failure_code, failure_message, completed_at, failed_at, created_at, updated_at`,
		paymentID,
		attemptCount,
		now,
	))
}

func (store *PostgresStore) MarkCompleted(ctx context.Context, paymentID string, result RailResult, now time.Time) (Attempt, error) {
	completedAt := result.CompletedAt
	if completedAt.IsZero() {
		completedAt = now
	}
	return scanAttempt(store.db.QueryRow(ctx, `
UPDATE payment_attempts
SET status = 'COMPLETED',
  processor_payment_id = $2,
  completed_at = COALESCE(NULLIF($3::timestamptz, '0001-01-01 00:00:00+00'::timestamptz), now()),
  updated_at = COALESCE(NULLIF($4::timestamptz, '0001-01-01 00:00:00+00'::timestamptz), now())
WHERE payment_id = $1
RETURNING id::text, payment_id, idempotency_key, trace_id, amount_cents, currency,
  ledger_reservation_id, wallet_debit_id, status, attempt_count, processor_payment_id,
  failure_code, failure_message, completed_at, failed_at, created_at, updated_at`,
		paymentID,
		result.ProcessorPaymentID,
		completedAt,
		now,
	))
}

func (store *PostgresStore) MarkFailed(ctx context.Context, paymentID string, failure RailFailure, now time.Time) (Attempt, error) {
	return scanAttempt(store.db.QueryRow(ctx, `
UPDATE payment_attempts
SET status = 'FAILED',
  failure_code = $2,
  failure_message = $3,
  failed_at = COALESCE(NULLIF($4::timestamptz, '0001-01-01 00:00:00+00'::timestamptz), now()),
  updated_at = COALESCE(NULLIF($5::timestamptz, '0001-01-01 00:00:00+00'::timestamptz), now())
WHERE payment_id = $1
RETURNING id::text, payment_id, idempotency_key, trace_id, amount_cents, currency,
  ledger_reservation_id, wallet_debit_id, status, attempt_count, processor_payment_id,
  failure_code, failure_message, completed_at, failed_at, created_at, updated_at`,
		paymentID,
		failure.Code,
		failure.Message,
		now,
		now,
	))
}

const paymentAttemptSelectSQL = `
SELECT id::text, payment_id, idempotency_key, trace_id, amount_cents, currency,
  ledger_reservation_id, wallet_debit_id, status, attempt_count, processor_payment_id,
  failure_code, failure_message, completed_at, failed_at, created_at, updated_at
FROM payment_attempts`

type attemptRow interface {
	Scan(...any) error
}

func scanAttempt(row attemptRow) (Attempt, error) {
	var attempt Attempt
	var completedAt *time.Time
	var failedAt *time.Time
	if err := row.Scan(
		&attempt.ID,
		&attempt.PaymentID,
		&attempt.IdempotencyKey,
		&attempt.TraceID,
		&attempt.AmountCents,
		&attempt.Currency,
		&attempt.LedgerReservationID,
		&attempt.WalletDebitID,
		&attempt.Status,
		&attempt.AttemptCount,
		&attempt.ProcessorPaymentID,
		&attempt.FailureCode,
		&attempt.FailureMessage,
		&completedAt,
		&failedAt,
		&attempt.CreatedAt,
		&attempt.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Attempt{}, ErrNotFound
		}
		return Attempt{}, err
	}
	if completedAt != nil {
		attempt.CompletedAt = *completedAt
	}
	if failedAt != nil {
		attempt.FailedAt = *failedAt
	}
	return attempt, nil
}
