package repo

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"time"

	"enjoythings/services/internal/domain"
	"enjoythings/services/internal/event"
	"enjoythings/services/internal/outbox"
	"enjoythings/services/internal/repo/queries"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

type LedgerCursor struct {
	CreatedAt time.Time
	ID        uuid.UUID
	Valid     bool
}

type LedgerTransfer struct {
	TransferID   uuid.UUID
	FromWalletID uuid.UUID
	ToWalletID   uuid.UUID
	AmountCents  int64
	Currency     string
}

func (db *Database) CreateWallet(ctx context.Context, userID uuid.UUID, currency string) (domain.Wallet, error) {
	wallet, err := db.queries.CreateWallet(ctx, queries.CreateWalletParams{
		UserID:   pgUUID(userID),
		Currency: currency,
	})
	if err != nil {
		return domain.Wallet{}, err
	}
	return walletFromQuery(wallet), nil
}

func (db *Database) GetWallet(ctx context.Context, id uuid.UUID) (domain.Wallet, error) {
	wallet, err := db.queries.GetWallet(ctx, pgUUID(id))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Wallet{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.Wallet{}, err
	}
	return walletFromQuery(wallet), nil
}

func (db *Database) CreateTransfer(ctx context.Context, userID, fromWalletID, toWalletID uuid.UUID, amount int64) (domain.Transfer, error) {
	if amount <= 0 {
		return domain.Transfer{}, domain.ErrInvalidAmount
	}
	if fromWalletID == toWalletID {
		return domain.Transfer{}, domain.ErrInvalidTransfer
	}

	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return domain.Transfer{}, err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	locked, err := lockWallets(ctx, tx, fromWalletID, toWalletID)
	if err != nil {
		return domain.Transfer{}, err
	}
	from, ok := locked[fromWalletID]
	if !ok || from.UserID != userID {
		return domain.Transfer{}, domain.ErrNotFound
	}
	to, ok := locked[toWalletID]
	if !ok {
		return domain.Transfer{}, domain.ErrNotFound
	}
	if from.Currency != to.Currency {
		return domain.Transfer{}, domain.ErrCurrencyMismatch
	}
	if err := from.Debit(amount); err != nil {
		return domain.Transfer{}, err
	}
	if err := to.Credit(amount); err != nil {
		return domain.Transfer{}, err
	}

	qtx := db.queries.WithTx(tx)
	if _, err := qtx.UpdateWalletBalance(ctx, queries.UpdateWalletBalanceParams{ID: pgUUID(from.ID), Balance: from.Balance}); err != nil {
		return domain.Transfer{}, err
	}
	if _, err := qtx.UpdateWalletBalance(ctx, queries.UpdateWalletBalanceParams{ID: pgUUID(to.ID), Balance: to.Balance}); err != nil {
		return domain.Transfer{}, err
	}

	createdTransfer, err := qtx.CreateTransfer(ctx, queries.CreateTransferParams{
		FromWalletID: pgUUID(fromWalletID),
		ToWalletID:   pgUUID(toWalletID),
		Amount:       amount,
	})
	if err != nil {
		return domain.Transfer{}, err
	}

	transfer := transferFromQuery(createdTransfer)
	payload, err := event.MarshalTransactionInitiated(transfer, from.Currency)
	if err != nil {
		return domain.Transfer{}, err
	}
	topic := db.outboxTopic
	if topic == "" {
		topic = event.TransactionInitiatedTopic
	}
	if _, err := outbox.NewRepository(tx).Enqueue(ctx, topic, from.ID.String(), payload); err != nil {
		return domain.Transfer{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return domain.Transfer{}, err
	}
	transfer.FromBalance = from.Balance
	transfer.ToBalance = to.Balance
	return transfer, nil
}

func (db *Database) DebitForSaga(ctx context.Context, cmd domain.SagaDebitCommand) (domain.SagaWalletOperation, error) {
	if cmd.PaymentID == uuid.Nil || cmd.FromWalletID == uuid.Nil || cmd.IdempotencyKey == "" {
		return domain.SagaWalletOperation{}, domain.ErrInvalidTransfer
	}
	if cmd.AmountCents <= 0 {
		return domain.SagaWalletOperation{}, domain.ErrInvalidAmount
	}
	if _, err := domain.NormalizeCurrency(cmd.Currency); err != nil {
		return domain.SagaWalletOperation{}, err
	}

	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return domain.SagaWalletOperation{}, err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	qtx := db.queries.WithTx(tx)
	if existing, err := getSagaWalletOperation(ctx, qtx, cmd.PaymentID, domain.SagaWalletOperationDebit); err == nil {
		if err := tx.Commit(ctx); err != nil {
			return domain.SagaWalletOperation{}, err
		}
		return existing, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return domain.SagaWalletOperation{}, err
	}

	wallet, err := lockWallet(ctx, tx, cmd.FromWalletID)
	if err != nil {
		return domain.SagaWalletOperation{}, err
	}
	if existing, err := getSagaWalletOperation(ctx, qtx, cmd.PaymentID, domain.SagaWalletOperationDebit); err == nil {
		if err := tx.Commit(ctx); err != nil {
			return domain.SagaWalletOperation{}, err
		}
		return existing, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return domain.SagaWalletOperation{}, err
	}
	if wallet.ID == uuid.Nil {
		return domain.SagaWalletOperation{}, domain.ErrNotFound
	}
	if wallet.Currency != cmd.Currency {
		return domain.SagaWalletOperation{}, domain.ErrCurrencyMismatch
	}
	if err := wallet.Debit(cmd.AmountCents); err != nil {
		return domain.SagaWalletOperation{}, err
	}

	if _, err := qtx.UpdateWalletBalance(ctx, queries.UpdateWalletBalanceParams{ID: pgUUID(wallet.ID), Balance: wallet.Balance}); err != nil {
		return domain.SagaWalletOperation{}, err
	}
	created, err := qtx.CreateSagaWalletOperation(ctx, queries.CreateSagaWalletOperationParams{
		PaymentID:         pgUUID(cmd.PaymentID),
		Operation:         domain.SagaWalletOperationDebit,
		IdempotencyKey:    cmd.IdempotencyKey,
		WalletID:          pgUUID(wallet.ID),
		AmountCents:       cmd.AmountCents,
		Currency:          cmd.Currency,
		Status:            domain.SagaWalletOperationCompleted,
		BalanceAfterCents: wallet.Balance,
	})
	if err != nil {
		if isUniqueViolation(err) {
			_ = tx.Rollback(ctx)
			existing, lookupErr := db.getSagaWalletOperation(ctx, cmd.PaymentID, domain.SagaWalletOperationDebit)
			if errors.Is(lookupErr, pgx.ErrNoRows) {
				return domain.SagaWalletOperation{}, domain.ErrInvalidTransfer
			}
			return existing, lookupErr
		}
		return domain.SagaWalletOperation{}, err
	}
	op := sagaWalletOperationFromQuery(created)

	if err := tx.Commit(ctx); err != nil {
		return domain.SagaWalletOperation{}, err
	}
	return op, nil
}

func (db *Database) CompensateDebit(ctx context.Context, cmd domain.SagaCompensationCommand) (domain.SagaWalletOperation, error) {
	if cmd.PaymentID == uuid.Nil || cmd.FromWalletID == uuid.Nil || cmd.IdempotencyKey == "" {
		return domain.SagaWalletOperation{}, domain.ErrInvalidTransfer
	}
	if cmd.AmountCents < 0 {
		return domain.SagaWalletOperation{}, domain.ErrInvalidAmount
	}
	if cmd.Currency != "" {
		if _, err := domain.NormalizeCurrency(cmd.Currency); err != nil {
			return domain.SagaWalletOperation{}, err
		}
	}

	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return domain.SagaWalletOperation{}, err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	qtx := db.queries.WithTx(tx)
	if existing, err := getSagaWalletOperation(ctx, qtx, cmd.PaymentID, domain.SagaWalletOperationCompensation); err == nil {
		if err := tx.Commit(ctx); err != nil {
			return domain.SagaWalletOperation{}, err
		}
		return existing, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return domain.SagaWalletOperation{}, err
	}

	debit, err := getSagaWalletOperation(ctx, qtx, cmd.PaymentID, domain.SagaWalletOperationDebit)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.SagaWalletOperation{}, domain.ErrDebitNotFound
	}
	if err != nil {
		return domain.SagaWalletOperation{}, err
	}
	if existing, err := getSagaWalletOperation(ctx, qtx, cmd.PaymentID, domain.SagaWalletOperationCompensation); err == nil {
		if err := tx.Commit(ctx); err != nil {
			return domain.SagaWalletOperation{}, err
		}
		return existing, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return domain.SagaWalletOperation{}, err
	}
	if cmd.WalletDebitID != uuid.Nil && cmd.WalletDebitID != debit.ID {
		return domain.SagaWalletOperation{}, domain.ErrDebitNotFound
	}
	if debit.WalletID != cmd.FromWalletID {
		return domain.SagaWalletOperation{}, domain.ErrDebitNotFound
	}
	if cmd.AmountCents > 0 && cmd.AmountCents != debit.AmountCents {
		return domain.SagaWalletOperation{}, domain.ErrInvalidAmount
	}
	if cmd.Currency != "" && cmd.Currency != debit.Currency {
		return domain.SagaWalletOperation{}, domain.ErrCurrencyMismatch
	}

	wallet, err := lockWallet(ctx, tx, debit.WalletID)
	if err != nil {
		return domain.SagaWalletOperation{}, err
	}
	if wallet.ID == uuid.Nil {
		return domain.SagaWalletOperation{}, domain.ErrNotFound
	}
	if err := wallet.Credit(debit.AmountCents); err != nil {
		return domain.SagaWalletOperation{}, err
	}

	if _, err := qtx.UpdateWalletBalance(ctx, queries.UpdateWalletBalanceParams{ID: pgUUID(wallet.ID), Balance: wallet.Balance}); err != nil {
		return domain.SagaWalletOperation{}, err
	}
	created, err := qtx.CreateSagaWalletOperation(ctx, queries.CreateSagaWalletOperationParams{
		PaymentID:         pgUUID(cmd.PaymentID),
		Operation:         domain.SagaWalletOperationCompensation,
		IdempotencyKey:    cmd.IdempotencyKey,
		WalletID:          pgUUID(wallet.ID),
		AmountCents:       debit.AmountCents,
		Currency:          debit.Currency,
		Status:            domain.SagaWalletOperationCompleted,
		BalanceAfterCents: wallet.Balance,
	})
	if err != nil {
		if isUniqueViolation(err) {
			_ = tx.Rollback(ctx)
			existing, lookupErr := db.getSagaWalletOperation(ctx, cmd.PaymentID, domain.SagaWalletOperationCompensation)
			if errors.Is(lookupErr, pgx.ErrNoRows) {
				return domain.SagaWalletOperation{}, domain.ErrInvalidTransfer
			}
			return existing, lookupErr
		}
		return domain.SagaWalletOperation{}, err
	}
	op := sagaWalletOperationFromQuery(created)

	if err := tx.Commit(ctx); err != nil {
		return domain.SagaWalletOperation{}, err
	}
	return op, nil
}

func (db *Database) ListLedgerEntries(ctx context.Context, walletID uuid.UUID, cursor LedgerCursor, limit int) ([]domain.LedgerEntry, LedgerCursor, error) {
	if limit < 1 {
		return nil, LedgerCursor{}, domain.ErrInvalidAmount
	}
	queryLimit := limit + 1
	params := queries.ListLedgerEntriesParams{
		WalletID: pgUUID(walletID),
		Limit:    int32(queryLimit),
	}
	if cursor.Valid {
		params.CursorCreatedAt = pgtype.Timestamptz{Time: cursor.CreatedAt, Valid: true}
		params.CursorID = pgUUID(cursor.ID)
	}
	rows, err := db.queries.ListLedgerEntries(ctx, params)
	if err != nil {
		return nil, LedgerCursor{}, err
	}
	entries := ledgerEntriesFromQuery(rows)

	next := LedgerCursor{}
	if len(entries) > limit {
		entries = entries[:limit]
		last := entries[len(entries)-1]
		next = LedgerCursor{CreatedAt: last.CreatedAt, ID: last.ID, Valid: true}
	}
	return entries, next, nil
}

func (db *Database) GetFraudTransactionHistory(ctx context.Context, walletID uuid.UUID, limit int, _ string) ([]domain.FraudTransactionSummary, error) {
	if limit < 1 || limit > 100 {
		return nil, domain.ErrInvalidAmount
	}
	if _, err := db.GetWallet(ctx, walletID); err != nil {
		return nil, err
	}
	entries, err := db.queries.ListFraudTransactionHistory(ctx, queries.ListFraudTransactionHistoryParams{
		WalletID: pgUUID(walletID),
		Limit:    int32(limit),
	})
	if err != nil {
		return nil, err
	}
	summaries := make([]domain.FraudTransactionSummary, 0, len(entries))
	for _, entry := range entries {
		summaries = append(summaries, domain.FraudTransactionSummary{
			Direction:   entry.Direction,
			AmountCents: entry.Amount,
			Currency:    entry.Currency,
			OccurredAt:  entry.CreatedAt.Time,
		})
	}
	return summaries, nil
}

func (db *Database) GetFraudVelocityMetrics(ctx context.Context, walletID uuid.UUID, asOf time.Time, _ string) (domain.FraudVelocityMetrics, error) {
	if _, err := db.GetWallet(ctx, walletID); err != nil {
		return domain.FraudVelocityMetrics{}, err
	}
	row, err := db.queries.GetFraudVelocityMetrics(ctx, queries.GetFraudVelocityMetricsParams{
		FraudWalletID: pgUUID(walletID),
		AsOf:          pgtype.Timestamptz{Time: asOf, Valid: true},
	})
	if err != nil {
		return domain.FraudVelocityMetrics{}, err
	}
	return domain.FraudVelocityMetrics{
		TransactionsLastHour:  row.TransactionsLastHour,
		AmountLastHourCents:   row.AmountLastHourCents,
		AverageAmount30dCents: row.AverageAmount30dCents,
		DistinctRecipients30d: row.DistinctRecipients30d,
	}, nil
}

func (db *Database) TransferProcessed(ctx context.Context, transferID uuid.UUID) (bool, error) {
	var exists bool
	err := db.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM ledger_entries WHERE transfer_id = $1)`, pgUUID(transferID)).Scan(&exists)
	return exists, err
}

func (db *Database) AppendTransferEntries(ctx context.Context, transfer LedgerTransfer) error {
	if transfer.AmountCents <= 0 {
		return domain.ErrInvalidAmount
	}
	if transfer.FromWalletID == transfer.ToWalletID {
		return domain.ErrInvalidTransfer
	}
	if _, err := domain.NormalizeCurrency(transfer.Currency); err != nil {
		return err
	}

	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	var processed bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM ledger_entries WHERE transfer_id = $1)`, pgUUID(transfer.TransferID)).Scan(&processed); err != nil {
		return err
	}
	if processed {
		return tx.Commit(ctx)
	}

	locked, err := lockWallets(ctx, tx, transfer.FromWalletID, transfer.ToWalletID)
	if err != nil {
		return err
	}
	from, ok := locked[transfer.FromWalletID]
	if !ok {
		return domain.ErrNotFound
	}
	to, ok := locked[transfer.ToWalletID]
	if !ok {
		return domain.ErrNotFound
	}
	if from.Currency != to.Currency || from.Currency != transfer.Currency {
		return domain.ErrCurrencyMismatch
	}

	qtx := db.queries.WithTx(tx)
	if _, err := qtx.CreateLedgerEntry(ctx, queries.CreateLedgerEntryParams{
		WalletID:     pgUUID(from.ID),
		TransferID:   pgUUID(transfer.TransferID),
		Direction:    "debit",
		Amount:       transfer.AmountCents,
		BalanceAfter: from.Balance,
	}); err != nil {
		if isUniqueViolation(err) {
			return nil
		}
		return err
	}
	if _, err := qtx.CreateLedgerEntry(ctx, queries.CreateLedgerEntryParams{
		WalletID:     pgUUID(to.ID),
		TransferID:   pgUUID(transfer.TransferID),
		Direction:    "credit",
		Amount:       transfer.AmountCents,
		BalanceAfter: to.Balance,
	}); err != nil {
		if isUniqueViolation(err) {
			return nil
		}
		return err
	}

	return tx.Commit(ctx)
}

func (db *Database) ReserveTransfer(ctx context.Context, cmd domain.LedgerReserveCommand) (domain.LedgerReservation, error) {
	if cmd.PaymentID == uuid.Nil || cmd.FromWalletID == uuid.Nil || cmd.ToWalletID == uuid.Nil || cmd.IdempotencyKey == "" {
		return domain.LedgerReservation{}, domain.ErrInvalidTransfer
	}
	if cmd.AmountCents <= 0 {
		return domain.LedgerReservation{}, domain.ErrInvalidAmount
	}
	if cmd.FromWalletID == cmd.ToWalletID {
		return domain.LedgerReservation{}, domain.ErrInvalidTransfer
	}
	currency, err := domain.NormalizeCurrency(cmd.Currency)
	if err != nil {
		return domain.LedgerReservation{}, err
	}
	cmd.Currency = currency

	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return domain.LedgerReservation{}, err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	existing, err := getLedgerReservationByPaymentID(ctx, tx, cmd.PaymentID)
	if err == nil {
		if !sameReservationPayload(existing, cmd) {
			return domain.LedgerReservation{}, domain.ErrAlreadyExists
		}
		if err := tx.Commit(ctx); err != nil {
			return domain.LedgerReservation{}, err
		}
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return domain.LedgerReservation{}, err
	}

	locked, err := lockWallets(ctx, tx, cmd.FromWalletID, cmd.ToWalletID)
	if err != nil {
		return domain.LedgerReservation{}, err
	}
	from, ok := locked[cmd.FromWalletID]
	if !ok {
		return domain.LedgerReservation{}, domain.ErrNotFound
	}
	to, ok := locked[cmd.ToWalletID]
	if !ok {
		return domain.LedgerReservation{}, domain.ErrNotFound
	}
	if from.Currency != to.Currency || from.Currency != cmd.Currency {
		return domain.LedgerReservation{}, domain.ErrCurrencyMismatch
	}

	row := tx.QueryRow(ctx, `
INSERT INTO ledger_transfer_reservations (
  payment_id, idempotency_key, trace_id, from_wallet_id, to_wallet_id, amount_cents, currency, status
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING id, payment_id, idempotency_key, trace_id, from_wallet_id, to_wallet_id, amount_cents, currency, status,
  transfer_id, wallet_debit_id, completed_at, canceled_at, cancel_reason, created_at, updated_at`,
		pgUUID(cmd.PaymentID),
		cmd.IdempotencyKey,
		cmd.TraceID,
		pgUUID(cmd.FromWalletID),
		pgUUID(cmd.ToWalletID),
		cmd.AmountCents,
		cmd.Currency,
		domain.LedgerReservationReserved,
	)
	reservation, err := scanLedgerReservation(row)
	if err != nil {
		if isUniqueViolation(err) {
			_ = tx.Rollback(ctx)
			existing, lookupErr := db.getLedgerReservationByPaymentID(ctx, cmd.PaymentID)
			if lookupErr != nil {
				return domain.LedgerReservation{}, lookupErr
			}
			if !sameReservationPayload(existing, cmd) {
				return domain.LedgerReservation{}, domain.ErrAlreadyExists
			}
			return existing, nil
		}
		return domain.LedgerReservation{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.LedgerReservation{}, err
	}
	return reservation, nil
}

func (db *Database) ConfirmTransfer(ctx context.Context, cmd domain.LedgerConfirmCommand) (domain.LedgerConfirmation, error) {
	if cmd.PaymentID == uuid.Nil || cmd.LedgerReservationID == uuid.Nil || cmd.WalletDebitID == uuid.Nil || cmd.IdempotencyKey == "" {
		return domain.LedgerConfirmation{}, domain.ErrInvalidTransfer
	}

	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return domain.LedgerConfirmation{}, err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	reservation, err := getLedgerReservationByPaymentID(ctx, tx, cmd.PaymentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.LedgerConfirmation{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.LedgerConfirmation{}, err
	}
	if reservation.ID != cmd.LedgerReservationID {
		return domain.LedgerConfirmation{}, domain.ErrFailedPrecondition
	}
	switch reservation.Status {
	case domain.LedgerReservationConfirmed:
		if err := tx.Commit(ctx); err != nil {
			return domain.LedgerConfirmation{}, err
		}
		return ledgerConfirmationFromReservation(reservation), nil
	case domain.LedgerReservationCanceled:
		return domain.LedgerConfirmation{}, domain.ErrFailedPrecondition
	}

	locked, err := lockWallets(ctx, tx, reservation.FromWalletID, reservation.ToWalletID)
	if err != nil {
		return domain.LedgerConfirmation{}, err
	}
	from, ok := locked[reservation.FromWalletID]
	if !ok {
		return domain.LedgerConfirmation{}, domain.ErrNotFound
	}
	to, ok := locked[reservation.ToWalletID]
	if !ok {
		return domain.LedgerConfirmation{}, domain.ErrNotFound
	}
	if from.Currency != to.Currency || from.Currency != reservation.Currency {
		return domain.LedgerConfirmation{}, domain.ErrCurrencyMismatch
	}

	qtx := db.queries.WithTx(tx)
	createdTransfer, err := qtx.CreateTransfer(ctx, queries.CreateTransferParams{
		FromWalletID: pgUUID(reservation.FromWalletID),
		ToWalletID:   pgUUID(reservation.ToWalletID),
		Amount:       reservation.AmountCents,
	})
	if err != nil {
		return domain.LedgerConfirmation{}, err
	}
	transfer := transferFromQuery(createdTransfer)
	if _, err := qtx.CreateLedgerEntry(ctx, queries.CreateLedgerEntryParams{
		WalletID:     pgUUID(from.ID),
		TransferID:   pgUUID(transfer.ID),
		Direction:    "debit",
		Amount:       reservation.AmountCents,
		BalanceAfter: from.Balance,
	}); err != nil {
		return domain.LedgerConfirmation{}, err
	}
	if _, err := qtx.CreateLedgerEntry(ctx, queries.CreateLedgerEntryParams{
		WalletID:     pgUUID(to.ID),
		TransferID:   pgUUID(transfer.ID),
		Direction:    "credit",
		Amount:       reservation.AmountCents,
		BalanceAfter: to.Balance,
	}); err != nil {
		return domain.LedgerConfirmation{}, err
	}

	row := tx.QueryRow(ctx, `
UPDATE ledger_transfer_reservations
SET status = $2, transfer_id = $3, wallet_debit_id = $4, completed_at = now(), updated_at = now()
WHERE id = $1
RETURNING id, payment_id, idempotency_key, trace_id, from_wallet_id, to_wallet_id, amount_cents, currency, status,
  transfer_id, wallet_debit_id, completed_at, canceled_at, cancel_reason, created_at, updated_at`,
		pgUUID(reservation.ID),
		domain.LedgerReservationConfirmed,
		pgUUID(transfer.ID),
		pgUUID(cmd.WalletDebitID),
	)
	confirmed, err := scanLedgerReservation(row)
	if err != nil {
		return domain.LedgerConfirmation{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.LedgerConfirmation{}, err
	}
	return ledgerConfirmationFromReservation(confirmed), nil
}

func (db *Database) CancelReservation(ctx context.Context, cmd domain.LedgerCancelCommand) (domain.LedgerReservation, error) {
	if cmd.PaymentID == uuid.Nil || cmd.LedgerReservationID == uuid.Nil || cmd.IdempotencyKey == "" {
		return domain.LedgerReservation{}, domain.ErrInvalidTransfer
	}

	tx, err := db.pool.Begin(ctx)
	if err != nil {
		return domain.LedgerReservation{}, err
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	reservation, err := getLedgerReservationByPaymentID(ctx, tx, cmd.PaymentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.LedgerReservation{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.LedgerReservation{}, err
	}
	if reservation.ID != cmd.LedgerReservationID {
		return domain.LedgerReservation{}, domain.ErrFailedPrecondition
	}
	switch reservation.Status {
	case domain.LedgerReservationCanceled:
		if err := tx.Commit(ctx); err != nil {
			return domain.LedgerReservation{}, err
		}
		return reservation, nil
	case domain.LedgerReservationConfirmed:
		return domain.LedgerReservation{}, domain.ErrFailedPrecondition
	}

	row := tx.QueryRow(ctx, `
UPDATE ledger_transfer_reservations
SET status = $2, canceled_at = now(), cancel_reason = $3, updated_at = now()
WHERE id = $1
RETURNING id, payment_id, idempotency_key, trace_id, from_wallet_id, to_wallet_id, amount_cents, currency, status,
  transfer_id, wallet_debit_id, completed_at, canceled_at, cancel_reason, created_at, updated_at`,
		pgUUID(reservation.ID),
		domain.LedgerReservationCanceled,
		cmd.Reason,
	)
	canceled, err := scanLedgerReservation(row)
	if err != nil {
		return domain.LedgerReservation{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.LedgerReservation{}, err
	}
	return canceled, nil
}

func (db *Database) RunMigrations(ctx context.Context) error {
	if _, err := db.pool.Exec(ctx, `SELECT pg_advisory_lock(hashtext('enjoythings_services_migrations'))`); err != nil {
		return err
	}
	defer func() {
		_, _ = db.pool.Exec(context.Background(), `SELECT pg_advisory_unlock(hashtext('enjoythings_services_migrations'))`)
	}()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return errors.New("resolve migration path")
	}
	path := filepath.Join(filepath.Dir(file), "..", "..", "db", "migrations", "000001_enable_pgcrypto.up.sql")
	migrationDir := filepath.Dir(path)
	files, err := filepath.Glob(filepath.Join(migrationDir, "*.up.sql"))
	if err != nil {
		return err
	}
	sort.Strings(files)
	for _, file := range files {
		sql, err := os.ReadFile(file)
		if err != nil {
			return err
		}
		if _, err := db.pool.Exec(ctx, string(sql)); err != nil {
			return err
		}
	}
	return nil
}

func (db *Database) Truncate(ctx context.Context) error {
	_, err := db.pool.Exec(ctx, `TRUNCATE payment_attempts, sagas, saga_wallet_operations, ledger_transfer_reservations, outbox_events, ledger_entries, transfers, wallets RESTART IDENTITY CASCADE`)
	return err
}

func (db *Database) SetWalletBalanceForTest(ctx context.Context, walletID uuid.UUID, balance int64) error {
	_, err := db.queries.UpdateWalletBalance(ctx, queries.UpdateWalletBalanceParams{ID: pgUUID(walletID), Balance: balance})
	return err
}

func lockWallets(ctx context.Context, tx pgx.Tx, firstID, secondID uuid.UUID) (map[uuid.UUID]domain.Wallet, error) {
	ids := []uuid.UUID{firstID, secondID}
	sort.Slice(ids, func(i, j int) bool {
		return ids[i].String() < ids[j].String()
	})
	pgIDs := make([]pgtype.UUID, 0, len(ids))
	for _, id := range ids {
		pgIDs = append(pgIDs, pgUUID(id))
	}
	rows, err := queries.New(tx).LockWalletsForTransfer(ctx, pgIDs)
	if err != nil {
		return nil, err
	}
	wallets := make(map[uuid.UUID]domain.Wallet, 2)
	for _, row := range rows {
		wallet := walletFromQuery(row)
		wallets[wallet.ID] = wallet
	}
	if len(wallets) != 2 {
		return wallets, nil
	}
	return wallets, nil
}

func lockWallet(ctx context.Context, tx pgx.Tx, id uuid.UUID) (domain.Wallet, error) {
	row, err := queries.New(tx).LockWalletForUpdate(ctx, pgUUID(id))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Wallet{}, nil
	}
	if err != nil {
		return domain.Wallet{}, err
	}
	return walletFromQuery(row), nil
}

func (db *Database) getSagaWalletOperation(ctx context.Context, paymentID uuid.UUID, operation string) (domain.SagaWalletOperation, error) {
	row, err := db.queries.GetSagaWalletOperation(ctx, queries.GetSagaWalletOperationParams{
		PaymentID: pgUUID(paymentID),
		Operation: operation,
	})
	if err != nil {
		return domain.SagaWalletOperation{}, err
	}
	return sagaWalletOperationFromQuery(row), nil
}

func getSagaWalletOperation(ctx context.Context, qtx *queries.Queries, paymentID uuid.UUID, operation string) (domain.SagaWalletOperation, error) {
	row, err := qtx.GetSagaWalletOperationForUpdate(ctx, queries.GetSagaWalletOperationForUpdateParams{
		PaymentID: pgUUID(paymentID),
		Operation: operation,
	})
	if err != nil {
		return domain.SagaWalletOperation{}, err
	}
	return sagaWalletOperationFromQuery(row), nil
}

func (db *Database) getLedgerReservationByPaymentID(ctx context.Context, paymentID uuid.UUID) (domain.LedgerReservation, error) {
	return scanLedgerReservation(db.pool.QueryRow(ctx, `
SELECT id, payment_id, idempotency_key, trace_id, from_wallet_id, to_wallet_id, amount_cents, currency, status,
  transfer_id, wallet_debit_id, completed_at, canceled_at, cancel_reason, created_at, updated_at
FROM ledger_transfer_reservations
WHERE payment_id = $1`,
		pgUUID(paymentID),
	))
}

func getLedgerReservationByPaymentID(ctx context.Context, tx pgx.Tx, paymentID uuid.UUID) (domain.LedgerReservation, error) {
	return scanLedgerReservation(tx.QueryRow(ctx, `
SELECT id, payment_id, idempotency_key, trace_id, from_wallet_id, to_wallet_id, amount_cents, currency, status,
  transfer_id, wallet_debit_id, completed_at, canceled_at, cancel_reason, created_at, updated_at
FROM ledger_transfer_reservations
WHERE payment_id = $1
FOR UPDATE`,
		pgUUID(paymentID),
	))
}

type ledgerReservationScanner interface {
	Scan(dest ...any) error
}

func scanLedgerReservation(row ledgerReservationScanner) (domain.LedgerReservation, error) {
	var reservation domain.LedgerReservation
	var id pgtype.UUID
	var paymentID pgtype.UUID
	var fromWalletID pgtype.UUID
	var toWalletID pgtype.UUID
	var transferID pgtype.UUID
	var walletDebitID pgtype.UUID
	var completedAt pgtype.Timestamptz
	var canceledAt pgtype.Timestamptz
	var createdAt pgtype.Timestamptz
	var updatedAt pgtype.Timestamptz
	if err := row.Scan(
		&id,
		&paymentID,
		&reservation.IdempotencyKey,
		&reservation.TraceID,
		&fromWalletID,
		&toWalletID,
		&reservation.AmountCents,
		&reservation.Currency,
		&reservation.Status,
		&transferID,
		&walletDebitID,
		&completedAt,
		&canceledAt,
		&reservation.CancelReason,
		&createdAt,
		&updatedAt,
	); err != nil {
		return domain.LedgerReservation{}, err
	}
	reservation.ID = uuidFromPG(id)
	reservation.PaymentID = uuidFromPG(paymentID)
	reservation.FromWalletID = uuidFromPG(fromWalletID)
	reservation.ToWalletID = uuidFromPG(toWalletID)
	reservation.TransferID = uuidFromPG(transferID)
	reservation.WalletDebitID = uuidFromPG(walletDebitID)
	if completedAt.Valid {
		reservation.CompletedAt = completedAt.Time
	}
	if canceledAt.Valid {
		reservation.CanceledAt = canceledAt.Time
	}
	if createdAt.Valid {
		reservation.CreatedAt = createdAt.Time
	}
	if updatedAt.Valid {
		reservation.UpdatedAt = updatedAt.Time
	}
	return reservation, nil
}

func sameReservationPayload(existing domain.LedgerReservation, cmd domain.LedgerReserveCommand) bool {
	return existing.PaymentID == cmd.PaymentID &&
		existing.IdempotencyKey == cmd.IdempotencyKey &&
		existing.FromWalletID == cmd.FromWalletID &&
		existing.ToWalletID == cmd.ToWalletID &&
		existing.AmountCents == cmd.AmountCents &&
		existing.Currency == cmd.Currency
}

func ledgerConfirmationFromReservation(reservation domain.LedgerReservation) domain.LedgerConfirmation {
	return domain.LedgerConfirmation{
		PaymentID:   reservation.PaymentID,
		TransferID:  reservation.TransferID,
		Status:      reservation.Status,
		CompletedAt: reservation.CompletedAt,
	}
}

func (cursor LedgerCursor) String() string {
	if !cursor.Valid {
		return ""
	}
	return fmt.Sprintf("%s/%s", cursor.CreatedAt.Format(time.RFC3339Nano), cursor.ID)
}

func pgUUID(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}

func uuidFromPG(id pgtype.UUID) uuid.UUID {
	if !id.Valid {
		return uuid.Nil
	}
	return uuid.UUID(id.Bytes)
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func walletFromQuery(wallet queries.Wallet) domain.Wallet {
	return domain.Wallet{
		ID:        uuidFromPG(wallet.ID),
		UserID:    uuidFromPG(wallet.UserID),
		Balance:   wallet.Balance,
		Currency:  wallet.Currency,
		CreatedAt: wallet.CreatedAt.Time,
		UpdatedAt: wallet.UpdatedAt.Time,
	}
}

func transferFromQuery(transfer queries.Transfer) domain.Transfer {
	return domain.Transfer{
		ID:           uuidFromPG(transfer.ID),
		FromWalletID: uuidFromPG(transfer.FromWalletID),
		ToWalletID:   uuidFromPG(transfer.ToWalletID),
		Amount:       transfer.Amount,
		Status:       transfer.Status,
		CreatedAt:    transfer.CreatedAt.Time,
	}
}

func sagaWalletOperationFromQuery(operation queries.SagaWalletOperation) domain.SagaWalletOperation {
	return domain.SagaWalletOperation{
		ID:                uuidFromPG(operation.ID),
		PaymentID:         uuidFromPG(operation.PaymentID),
		Operation:         operation.Operation,
		IdempotencyKey:    operation.IdempotencyKey,
		WalletID:          uuidFromPG(operation.WalletID),
		AmountCents:       operation.AmountCents,
		Currency:          operation.Currency,
		Status:            operation.Status,
		BalanceAfterCents: operation.BalanceAfterCents,
		CreatedAt:         operation.CreatedAt.Time,
		UpdatedAt:         operation.UpdatedAt.Time,
	}
}

func ledgerEntriesFromQuery(rows []queries.LedgerEntry) []domain.LedgerEntry {
	entries := make([]domain.LedgerEntry, 0, len(rows))
	for _, row := range rows {
		entries = append(entries, domain.LedgerEntry{
			ID:           uuidFromPG(row.ID),
			WalletID:     uuidFromPG(row.WalletID),
			TransferID:   uuidFromPG(row.TransferID),
			Direction:    row.Direction,
			Amount:       row.Amount,
			BalanceAfter: row.BalanceAfter,
			CreatedAt:    row.CreatedAt.Time,
		})
	}
	return entries
}
