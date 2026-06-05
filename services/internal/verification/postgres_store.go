package verification

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type PostgresStore struct {
	pool *pgxpool.Pool
}

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore {
	return &PostgresStore{pool: pool}
}

func (store *PostgresStore) Submit(ctx context.Context, cmd SubmitCommand, decision Decision, now time.Time) (SubmitResult, error) {
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return SubmitResult{}, err
	}
	defer tx.Rollback(ctx)

	if owner, err := idempotencyKeyOwner(ctx, tx, cmd.IdempotencyKey); err == nil && owner != cmd.UserID {
		return SubmitResult{}, ErrIdempotencyKeyConflict
	} else if err != nil && !errors.Is(err, ErrNotFound) {
		return SubmitResult{}, err
	}

	existing, err := selectVerificationForUpdate(ctx, tx, cmd.UserID)
	switch {
	case err == nil:
		result, err := updateExistingVerification(ctx, tx, existing, cmd, decision, now)
		if err != nil {
			return SubmitResult{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return SubmitResult{}, err
		}
		return result, nil
	case errors.Is(err, ErrNotFound):
		result, err := insertVerification(ctx, tx, cmd, decision, now)
		if err != nil {
			if isUniqueViolation(err) {
				return SubmitResult{}, ErrIdempotencyKeyConflict
			}
			return SubmitResult{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return SubmitResult{}, err
		}
		return result, nil
	default:
		return SubmitResult{}, err
	}
}

func (store *PostgresStore) GetStatus(ctx context.Context, userID string) (Record, error) {
	row, err := scanVerification(store.pool.QueryRow(ctx, verificationSelectSQL+` WHERE user_id = $1`, userID))
	if err != nil {
		return Record{}, err
	}
	return row.Record, nil
}

func (store *PostgresStore) Decide(ctx context.Context, cmd DecisionCommand, decision Decision, now time.Time) (SubmitResult, error) {
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		return SubmitResult{}, err
	}
	defer tx.Rollback(ctx)

	existing, err := selectVerificationForUpdate(ctx, tx, cmd.UserID)
	if err != nil {
		return SubmitResult{}, err
	}
	if existing.Status == StatusVerified || existing.Status == StatusRejected {
		if err := tx.Commit(ctx); err != nil {
			return SubmitResult{}, err
		}
		return SubmitResult{Record: existing.Record}, nil
	}
	row, err := scanVerification(tx.QueryRow(ctx, `
UPDATE verifications
SET trace_id = $2,
  status = $3,
  reason = $4,
  decided_at = $5,
  updated_at = $6
WHERE user_id = $1
RETURNING verification_id, user_id, idempotency_key, payment_id, trace_id, status, reason,
  decided_at, created_at, updated_at`,
		cmd.UserID,
		cmd.TraceID,
		decision.Status,
		decision.Reason,
		now,
		now,
	))
	if err != nil {
		return SubmitResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return SubmitResult{}, err
	}
	return SubmitResult{Record: row.Record, Transitioned: existing.Status != row.Status, TransitionedFrom: existing.Status}, nil
}

type verificationRow struct {
	Record
	IdempotencyKey string
	PaymentID      string
}

const verificationSelectSQL = `
SELECT verification_id, user_id, idempotency_key, payment_id, trace_id, status, reason,
  decided_at, created_at, updated_at
FROM verifications`

func selectVerificationForUpdate(ctx context.Context, tx pgx.Tx, userID string) (verificationRow, error) {
	return scanVerification(tx.QueryRow(ctx, verificationSelectSQL+` WHERE user_id = $1 FOR UPDATE`, userID))
}

func idempotencyKeyOwner(ctx context.Context, tx pgx.Tx, key string) (string, error) {
	var userID string
	err := tx.QueryRow(ctx, `SELECT user_id FROM verifications WHERE idempotency_key = $1`, key).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return userID, err
}

func insertVerification(ctx context.Context, tx pgx.Tx, cmd SubmitCommand, decision Decision, now time.Time) (SubmitResult, error) {
	verificationID := cmd.VerificationID
	if verificationID == "" {
		verificationID = newVerificationID()
	}
	decidedAt := decidedAtForStatus(decision.Status, now)
	row, err := scanVerification(tx.QueryRow(ctx, `
INSERT INTO verifications (
  user_id, verification_id, idempotency_key, payment_id, trace_id, status, reason,
  decided_at, created_at, updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
RETURNING verification_id, user_id, idempotency_key, payment_id, trace_id, status, reason,
  decided_at, created_at, updated_at`,
		cmd.UserID,
		verificationID,
		cmd.IdempotencyKey,
		cmd.PaymentID,
		cmd.TraceID,
		decision.Status,
		decision.Reason,
		decidedAt,
		now,
		now,
	))
	if err != nil {
		return SubmitResult{}, err
	}
	return SubmitResult{Record: row.Record, Transitioned: row.Status != StatusPending || decision.Status == StatusPending, TransitionedFrom: StatusUnverified}, nil
}

func updateExistingVerification(ctx context.Context, tx pgx.Tx, existing verificationRow, cmd SubmitCommand, decision Decision, now time.Time) (SubmitResult, error) {
	if existing.IdempotencyKey == cmd.IdempotencyKey || existing.Status == StatusVerified || existing.Status == StatusRejected {
		return SubmitResult{Record: existing.Record}, nil
	}
	decidedAt := existing.DecidedAt
	if !isTerminalEventStatus(existing.Status) && isTerminalEventStatus(decision.Status) {
		decidedAt = now
	}
	row, err := scanVerification(tx.QueryRow(ctx, `
UPDATE verifications
SET idempotency_key = $2,
  payment_id = $3,
  trace_id = $4,
  status = $5,
  reason = $6,
  decided_at = $7,
  updated_at = $8
WHERE user_id = $1
RETURNING verification_id, user_id, idempotency_key, payment_id, trace_id, status, reason,
  decided_at, created_at, updated_at`,
		cmd.UserID,
		cmd.IdempotencyKey,
		cmd.PaymentID,
		cmd.TraceID,
		decision.Status,
		decision.Reason,
		decidedAt,
		now,
	))
	if err != nil {
		if isUniqueViolation(err) {
			return SubmitResult{}, ErrIdempotencyKeyConflict
		}
		return SubmitResult{}, err
	}
	return SubmitResult{Record: row.Record, Transitioned: existing.Status != row.Status, TransitionedFrom: existing.Status}, nil
}

type verificationScanner interface {
	Scan(...any) error
}

func scanVerification(row verificationScanner) (verificationRow, error) {
	var scanned verificationRow
	var decidedAt *time.Time
	if err := row.Scan(
		&scanned.VerificationID,
		&scanned.UserID,
		&scanned.IdempotencyKey,
		&scanned.PaymentID,
		&scanned.TraceID,
		&scanned.Status,
		&scanned.Reason,
		&decidedAt,
		&scanned.CreatedAt,
		&scanned.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return verificationRow{}, ErrNotFound
		}
		return verificationRow{}, err
	}
	if decidedAt != nil {
		scanned.DecidedAt = *decidedAt
	}
	return scanned, nil
}

func decidedAtForStatus(status string, now time.Time) *time.Time {
	if !isTerminalEventStatus(status) {
		return nil
	}
	return &now
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
