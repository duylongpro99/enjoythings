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
	_, err := db.pool.Exec(ctx, `TRUNCATE sagas, saga_wallet_operations, outbox_events, ledger_entries, transfers, wallets RESTART IDENTITY CASCADE`)
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
