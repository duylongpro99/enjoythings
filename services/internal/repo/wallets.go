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

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type LedgerCursor struct {
	CreatedAt time.Time
	ID        uuid.UUID
	Valid     bool
}

func (db *Database) CreateWallet(ctx context.Context, userID uuid.UUID, currency string) (domain.Wallet, error) {
	var wallet domain.Wallet
	err := db.pool.QueryRow(ctx, `
		INSERT INTO wallets (user_id, currency)
		VALUES ($1, $2)
		RETURNING id, user_id, balance, currency, created_at, updated_at
	`, userID, currency).Scan(&wallet.ID, &wallet.UserID, &wallet.Balance, &wallet.Currency, &wallet.CreatedAt, &wallet.UpdatedAt)
	return wallet, err
}

func (db *Database) GetWallet(ctx context.Context, id uuid.UUID) (domain.Wallet, error) {
	wallet, err := scanWallet(db.pool.QueryRow(ctx, `
		SELECT id, user_id, balance, currency, created_at, updated_at
		FROM wallets
		WHERE id = $1
	`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Wallet{}, domain.ErrNotFound
	}
	return wallet, err
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

	if _, err := tx.Exec(ctx, `UPDATE wallets SET balance = $2, updated_at = now() WHERE id = $1`, from.ID, from.Balance); err != nil {
		return domain.Transfer{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE wallets SET balance = $2, updated_at = now() WHERE id = $1`, to.ID, to.Balance); err != nil {
		return domain.Transfer{}, err
	}

	var transfer domain.Transfer
	err = tx.QueryRow(ctx, `
		INSERT INTO transfers (from_wallet_id, to_wallet_id, amount)
		VALUES ($1, $2, $3)
		RETURNING id, from_wallet_id, to_wallet_id, amount, status, created_at
	`, fromWalletID, toWalletID, amount).Scan(
		&transfer.ID, &transfer.FromWalletID, &transfer.ToWalletID, &transfer.Amount,
		&transfer.Status, &transfer.CreatedAt,
	)
	if err != nil {
		return domain.Transfer{}, err
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO ledger_entries (wallet_id, transfer_id, direction, amount, balance_after)
		VALUES ($1, $2, 'debit', $3, $4)
	`, from.ID, transfer.ID, amount, from.Balance); err != nil {
		return domain.Transfer{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO ledger_entries (wallet_id, transfer_id, direction, amount, balance_after)
		VALUES ($1, $2, 'credit', $3, $4)
	`, to.ID, transfer.ID, amount, to.Balance); err != nil {
		return domain.Transfer{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return domain.Transfer{}, err
	}
	transfer.FromBalance = from.Balance
	transfer.ToBalance = to.Balance
	return transfer, nil
}

func (db *Database) ListLedgerEntries(ctx context.Context, walletID uuid.UUID, cursor LedgerCursor, limit int) ([]domain.LedgerEntry, LedgerCursor, error) {
	if limit < 1 {
		return nil, LedgerCursor{}, domain.ErrInvalidAmount
	}
	queryLimit := limit + 1
	args := []any{walletID, queryLimit}
	query := `
		SELECT id, wallet_id, transfer_id, direction, amount, balance_after, created_at
		FROM ledger_entries
		WHERE wallet_id = $1
	`
	if cursor.Valid {
		query += ` AND (created_at, id) < ($3, $4)`
		args = append(args, cursor.CreatedAt, cursor.ID)
	}
	query += ` ORDER BY created_at DESC, id DESC LIMIT $2`

	rows, err := db.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, LedgerCursor{}, err
	}
	defer rows.Close()

	entries := make([]domain.LedgerEntry, 0, limit)
	for rows.Next() {
		var entry domain.LedgerEntry
		if err := rows.Scan(&entry.ID, &entry.WalletID, &entry.TransferID, &entry.Direction, &entry.Amount, &entry.BalanceAfter, &entry.CreatedAt); err != nil {
			return nil, LedgerCursor{}, err
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, LedgerCursor{}, err
	}

	next := LedgerCursor{}
	if len(entries) > limit {
		entries = entries[:limit]
		last := entries[len(entries)-1]
		next = LedgerCursor{CreatedAt: last.CreatedAt, ID: last.ID, Valid: true}
	}
	return entries, next, nil
}

func (db *Database) RunMigrations(ctx context.Context) error {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return errors.New("resolve migration path")
	}
	path := filepath.Join(filepath.Dir(file), "..", "..", "db", "migrations", "000001_enable_pgcrypto.up.sql")
	sql, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	_, err = db.pool.Exec(ctx, string(sql))
	return err
}

func (db *Database) Truncate(ctx context.Context) error {
	_, err := db.pool.Exec(ctx, `TRUNCATE ledger_entries, transfers, wallets RESTART IDENTITY CASCADE`)
	return err
}

func (db *Database) SetWalletBalanceForTest(ctx context.Context, walletID uuid.UUID, balance int64) error {
	_, err := db.pool.Exec(ctx, `UPDATE wallets SET balance = $2, updated_at = now() WHERE id = $1`, walletID, balance)
	return err
}

type walletScanner interface {
	Scan(dest ...any) error
}

func scanWallet(row walletScanner) (domain.Wallet, error) {
	var wallet domain.Wallet
	err := row.Scan(&wallet.ID, &wallet.UserID, &wallet.Balance, &wallet.Currency, &wallet.CreatedAt, &wallet.UpdatedAt)
	return wallet, err
}

func lockWallets(ctx context.Context, tx pgx.Tx, firstID, secondID uuid.UUID) (map[uuid.UUID]domain.Wallet, error) {
	ids := []uuid.UUID{firstID, secondID}
	sort.Slice(ids, func(i, j int) bool {
		return ids[i].String() < ids[j].String()
	})
	rows, err := tx.Query(ctx, `
		SELECT id, user_id, balance, currency, created_at, updated_at
		FROM wallets
		WHERE id = ANY($1::uuid[])
		ORDER BY id
		FOR UPDATE
	`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	wallets := make(map[uuid.UUID]domain.Wallet, 2)
	for rows.Next() {
		var wallet domain.Wallet
		if err := rows.Scan(&wallet.ID, &wallet.UserID, &wallet.Balance, &wallet.Currency, &wallet.CreatedAt, &wallet.UpdatedAt); err != nil {
			return nil, err
		}
		wallets[wallet.ID] = wallet
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(wallets) != 2 {
		return wallets, nil
	}
	return wallets, nil
}

func (cursor LedgerCursor) String() string {
	if !cursor.Valid {
		return ""
	}
	return fmt.Sprintf("%s/%s", cursor.CreatedAt.Format(time.RFC3339Nano), cursor.ID)
}
