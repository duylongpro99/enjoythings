package saga

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type pgDB interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

type PostgresStore struct {
	db pgDB
}

func NewPostgresStore(db pgDB) *PostgresStore {
	return &PostgresStore{db: db}
}

func (store *PostgresStore) Create(ctx context.Context, saga Saga) (Saga, error) {
	row := store.db.QueryRow(ctx, `
INSERT INTO sagas (
  payment_id, idempotency_key, user_id, from_wallet_id, to_wallet_id,
  amount_cents, currency, state, last_error, trace_id,
  wallet_debit_id, ledger_reservation_id, transfer_id, failure_code,
  fraud_session_id, fraud_action, fraud_risk_score, fraud_reason,
  fraud_flagged_at, deferred_payment_json, created_at, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14,
  $15, $16, $17, $18, $19, $20,
  COALESCE(NULLIF($21::timestamptz, '0001-01-01 00:00:00+00'::timestamptz), now()),
  COALESCE(NULLIF($22::timestamptz, '0001-01-01 00:00:00+00'::timestamptz), now()))
RETURNING id::text, payment_id, idempotency_key, user_id, from_wallet_id, to_wallet_id,
  amount_cents, currency, state, last_error, trace_id, wallet_debit_id,
  ledger_reservation_id, transfer_id, failure_code, fraud_session_id, fraud_action,
  fraud_risk_score, fraud_reason, fraud_flagged_at, deferred_payment_json, created_at, updated_at`,
		saga.PaymentID,
		saga.IdempotencyKey,
		saga.UserID,
		saga.FromWalletID,
		saga.ToWalletID,
		saga.AmountCents,
		saga.Currency,
		saga.State,
		saga.LastError,
		saga.TraceID,
		saga.WalletDebitID,
		saga.LedgerReservationID,
		saga.TransferID,
		saga.FailureCode,
		saga.FraudSessionID,
		saga.FraudAction,
		saga.FraudRiskScore,
		saga.FraudReason,
		saga.FraudFlaggedAt,
		saga.DeferredPaymentJSON,
		saga.CreatedAt,
		saga.UpdatedAt,
	)
	created, err := scanSaga(row)
	if err == nil {
		return created, nil
	}
	if !isUniqueViolation(err) {
		return Saga{}, err
	}
	existing, err := store.findDuplicate(ctx, saga)
	if err != nil {
		return Saga{}, err
	}
	if sameStartPayload(existing, saga) {
		return existing, nil
	}
	return Saga{}, ErrAlreadyExists
}

func (store *PostgresStore) GetByPaymentID(ctx context.Context, paymentID string) (Saga, error) {
	return scanSaga(store.db.QueryRow(ctx, `
SELECT id::text, payment_id, idempotency_key, user_id, from_wallet_id, to_wallet_id,
  amount_cents, currency, state, last_error, trace_id, wallet_debit_id,
  ledger_reservation_id, transfer_id, failure_code, fraud_session_id, fraud_action,
  fraud_risk_score, fraud_reason, fraud_flagged_at, deferred_payment_json, created_at, updated_at
FROM sagas
WHERE payment_id = $1`, paymentID))
}

func (store *PostgresStore) ListNonTerminal(ctx context.Context) ([]Saga, error) {
	rows, err := store.db.Query(ctx, `
SELECT id::text, payment_id, idempotency_key, user_id, from_wallet_id, to_wallet_id,
  amount_cents, currency, state, last_error, trace_id, wallet_debit_id,
  ledger_reservation_id, transfer_id, failure_code, fraud_session_id, fraud_action,
  fraud_risk_score, fraud_reason, fraud_flagged_at, deferred_payment_json, created_at, updated_at
FROM sagas
WHERE state NOT IN ('COMPLETED', 'FAILED')
ORDER BY updated_at, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sagas []Saga
	for rows.Next() {
		current, err := scanSaga(rows)
		if err != nil {
			return nil, err
		}
		sagas = append(sagas, current)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return sagas, nil
}

func (store *PostgresStore) Update(ctx context.Context, saga Saga) (Saga, error) {
	return scanSaga(store.db.QueryRow(ctx, `
UPDATE sagas
SET state = $2,
  last_error = $3,
  trace_id = $4,
  wallet_debit_id = $5,
  ledger_reservation_id = $6,
  transfer_id = $7,
  failure_code = $8,
  fraud_session_id = $9,
  fraud_action = $10,
  fraud_risk_score = $11,
  fraud_reason = $12,
  fraud_flagged_at = $13,
  deferred_payment_json = $14,
  updated_at = COALESCE(NULLIF($15::timestamptz, '0001-01-01 00:00:00+00'::timestamptz), now())
WHERE payment_id = $1
RETURNING id::text, payment_id, idempotency_key, user_id, from_wallet_id, to_wallet_id,
  amount_cents, currency, state, last_error, trace_id, wallet_debit_id,
  ledger_reservation_id, transfer_id, failure_code, fraud_session_id, fraud_action,
  fraud_risk_score, fraud_reason, fraud_flagged_at, deferred_payment_json, created_at, updated_at`,
		saga.PaymentID,
		saga.State,
		saga.LastError,
		saga.TraceID,
		saga.WalletDebitID,
		saga.LedgerReservationID,
		saga.TransferID,
		saga.FailureCode,
		saga.FraudSessionID,
		saga.FraudAction,
		saga.FraudRiskScore,
		saga.FraudReason,
		saga.FraudFlaggedAt,
		saga.DeferredPaymentJSON,
		saga.UpdatedAt,
	))
}

func (store *PostgresStore) findDuplicate(ctx context.Context, saga Saga) (Saga, error) {
	if saga.PaymentID != "" {
		existing, err := store.GetByPaymentID(ctx, saga.PaymentID)
		if err == nil {
			return existing, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return Saga{}, err
		}
	}
	return scanSaga(store.db.QueryRow(ctx, `
SELECT id::text, payment_id, idempotency_key, user_id, from_wallet_id, to_wallet_id,
  amount_cents, currency, state, last_error, trace_id, wallet_debit_id,
  ledger_reservation_id, transfer_id, failure_code, fraud_session_id, fraud_action,
  fraud_risk_score, fraud_reason, fraud_flagged_at, deferred_payment_json, created_at, updated_at
FROM sagas
WHERE user_id = $1 AND command_type = 'StartPaymentSaga' AND idempotency_key = $2`,
		saga.UserID,
		saga.IdempotencyKey,
	))
}

type sagaRow interface {
	Scan(...any) error
}

func scanSaga(row sagaRow) (Saga, error) {
	var saga Saga
	if err := row.Scan(
		&saga.ID,
		&saga.PaymentID,
		&saga.IdempotencyKey,
		&saga.UserID,
		&saga.FromWalletID,
		&saga.ToWalletID,
		&saga.AmountCents,
		&saga.Currency,
		&saga.State,
		&saga.LastError,
		&saga.TraceID,
		&saga.WalletDebitID,
		&saga.LedgerReservationID,
		&saga.TransferID,
		&saga.FailureCode,
		&saga.FraudSessionID,
		&saga.FraudAction,
		&saga.FraudRiskScore,
		&saga.FraudReason,
		&saga.FraudFlaggedAt,
		&saga.DeferredPaymentJSON,
		&saga.CreatedAt,
		&saga.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Saga{}, ErrNotFound
		}
		return Saga{}, err
	}
	return saga, nil
}

func sameStartPayload(left, right Saga) bool {
	return left.PaymentID == right.PaymentID &&
		left.IdempotencyKey == right.IdempotencyKey &&
		left.UserID == right.UserID &&
		left.FromWalletID == right.FromWalletID &&
		left.ToWalletID == right.ToWalletID &&
		left.AmountCents == right.AmountCents &&
		left.Currency == right.Currency
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
