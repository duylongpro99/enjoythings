package repo

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"enjoythings/services/internal/domain"
	"enjoythings/services/internal/event"
	"enjoythings/services/internal/outbox"
	"enjoythings/services/internal/repo/queries"

	"github.com/google/uuid"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestPostgresContainerCanBePingedThroughPool(t *testing.T) {
	ctx := context.Background()
	db := newIntegrationDB(t, ctx)

	if err := db.Ping(ctx); err != nil {
		t.Fatalf("ping Postgres container: %v", err)
	}
}

func TestRepositoryCreatesAndReadsWallet(t *testing.T) {
	ctx := context.Background()
	db := newIntegrationDB(t, ctx)
	userID := uuid.New()

	wallet, err := db.CreateWallet(ctx, userID, "USD")
	if err != nil {
		t.Fatalf("CreateWallet: %v", err)
	}
	if wallet.ID == uuid.Nil {
		t.Fatal("wallet ID was not assigned")
	}
	if wallet.UserID != userID {
		t.Fatalf("UserID = %s, want %s", wallet.UserID, userID)
	}
	if wallet.Balance != 0 {
		t.Fatalf("Balance = %d, want 0", wallet.Balance)
	}

	got, err := db.GetWallet(ctx, wallet.ID)
	if err != nil {
		t.Fatalf("GetWallet: %v", err)
	}
	if got.ID != wallet.ID || got.UserID != userID || got.Currency != "USD" {
		t.Fatalf("GetWallet = %+v, want wallet for user %s", got, userID)
	}
}

func TestRepositoryTransferUpdatesBalancesAndDefersLedgerEntries(t *testing.T) {
	ctx := context.Background()
	db := newIntegrationDB(t, ctx)
	fromUserID := uuid.New()
	from := createWalletFixture(t, ctx, db, fromUserID, 5000)
	to := createWalletFixture(t, ctx, db, uuid.New(), 5000)

	transfer, err := db.CreateTransfer(ctx, fromUserID, from.ID, to.ID, 1250)
	if err != nil {
		t.Fatalf("CreateTransfer: %v", err)
	}
	if transfer.Amount != 1250 || transfer.FromBalance != 3750 || transfer.ToBalance != 6250 {
		t.Fatalf("transfer = %+v", transfer)
	}

	fromAfter, err := db.GetWallet(ctx, from.ID)
	if err != nil {
		t.Fatalf("GetWallet(from): %v", err)
	}
	toAfter, err := db.GetWallet(ctx, to.ID)
	if err != nil {
		t.Fatalf("GetWallet(to): %v", err)
	}
	if fromAfter.Balance != 3750 || toAfter.Balance != 6250 {
		t.Fatalf("balances = %d/%d, want 3750/6250", fromAfter.Balance, toAfter.Balance)
	}

	fromEntries, _, err := db.ListLedgerEntries(ctx, from.ID, LedgerCursor{}, 10)
	if err != nil {
		t.Fatalf("ListLedgerEntries(from): %v", err)
	}
	toEntries, _, err := db.ListLedgerEntries(ctx, to.ID, LedgerCursor{}, 10)
	if err != nil {
		t.Fatalf("ListLedgerEntries(to): %v", err)
	}
	if len(fromEntries) != 0 || len(toEntries) != 0 {
		t.Fatalf("CreateTransfer must defer ledger writes to the ledger Kafka consumer, got from=%+v to=%+v", fromEntries, toEntries)
	}
}

func TestRepositoryTransferCreatesUnpublishedOutboxEvent(t *testing.T) {
	ctx := context.Background()
	db := newIntegrationDB(t, ctx)
	fromUserID := uuid.New()
	from := createWalletFixture(t, ctx, db, fromUserID, 5000)
	to := createWalletFixture(t, ctx, db, uuid.New(), 5000)

	transfer, err := db.CreateTransfer(ctx, fromUserID, from.ID, to.ID, 1250)
	if err != nil {
		t.Fatalf("CreateTransfer: %v", err)
	}

	events, err := outbox.NewRepository(db.pool).ClaimUnpublished(ctx, 10)
	if err != nil {
		t.Fatalf("ClaimUnpublished: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("outbox events len = %d, want 1", len(events))
	}
	outboxEvent := events[0]
	if outboxEvent.Topic != "tx.initiated" {
		t.Fatalf("Topic = %q, want tx.initiated", outboxEvent.Topic)
	}
	if outboxEvent.PartitionKey != from.ID.String() {
		t.Fatalf("PartitionKey = %q, want %s", outboxEvent.PartitionKey, from.ID)
	}
	if outboxEvent.PublishedAt != nil {
		t.Fatalf("PublishedAt = %v, want nil", outboxEvent.PublishedAt)
	}

	var payload event.TransactionInitiated
	if err := json.Unmarshal(outboxEvent.Payload, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.TransferID != transfer.ID.String() ||
		payload.FromWalletID != from.ID.String() ||
		payload.ToWalletID != to.ID.String() ||
		payload.AmountCents != 1250 ||
		payload.Currency != "USD" ||
		!payload.InitiatedAt.Equal(transfer.CreatedAt) {
		t.Fatalf("payload = %+v, want transfer %s from %s to %s", payload, transfer.ID, from.ID, to.ID)
	}
}

func TestRepositoryDebitForSagaIsIdempotentAndDoesNotPublishOutbox(t *testing.T) {
	ctx := context.Background()
	db := newIntegrationDB(t, ctx)
	userID := uuid.New()
	from := createWalletFixture(t, ctx, db, userID, 5000)
	paymentID := uuid.New()

	first, err := db.DebitForSaga(ctx, domain.SagaDebitCommand{
		PaymentID:      paymentID,
		FromWalletID:   from.ID,
		AmountCents:    1250,
		Currency:       "USD",
		IdempotencyKey: "debit-key",
	})
	if err != nil {
		t.Fatalf("DebitForSaga first: %v", err)
	}
	second, err := db.DebitForSaga(ctx, domain.SagaDebitCommand{
		PaymentID:      paymentID,
		FromWalletID:   from.ID,
		AmountCents:    1250,
		Currency:       "USD",
		IdempotencyKey: "debit-key",
	})
	if err != nil {
		t.Fatalf("DebitForSaga duplicate: %v", err)
	}
	if first.ID != second.ID || first.BalanceAfterCents != 3750 || second.BalanceAfterCents != 3750 {
		t.Fatalf("operations = %+v/%+v, want same completed debit with balance 3750", first, second)
	}

	fromAfter, err := db.GetWallet(ctx, from.ID)
	if err != nil {
		t.Fatalf("GetWallet: %v", err)
	}
	if fromAfter.Balance != 3750 {
		t.Fatalf("source balance = %d, want 3750", fromAfter.Balance)
	}
	outboxEvents, err := outbox.NewRepository(db.pool).ClaimUnpublished(ctx, 10)
	if err != nil {
		t.Fatalf("ClaimUnpublished: %v", err)
	}
	if len(outboxEvents) != 0 {
		t.Fatalf("saga debit published outbox events: %+v", outboxEvents)
	}
}

func TestRepositoryDebitForSagaRollsBackOnInsufficientFunds(t *testing.T) {
	ctx := context.Background()
	db := newIntegrationDB(t, ctx)
	from := createWalletFixture(t, ctx, db, uuid.New(), 100)

	_, err := db.DebitForSaga(ctx, domain.SagaDebitCommand{
		PaymentID:      uuid.New(),
		FromWalletID:   from.ID,
		AmountCents:    101,
		Currency:       "USD",
		IdempotencyKey: "debit-key",
	})
	if err != domain.ErrInsufficientFunds {
		t.Fatalf("DebitForSaga error = %v, want %v", err, domain.ErrInsufficientFunds)
	}
	fromAfter, err := db.GetWallet(ctx, from.ID)
	if err != nil {
		t.Fatalf("GetWallet: %v", err)
	}
	if fromAfter.Balance != 100 {
		t.Fatalf("source balance = %d, want 100", fromAfter.Balance)
	}
	var operations int
	if err := db.pool.QueryRow(ctx, `SELECT count(*) FROM saga_wallet_operations`).Scan(&operations); err != nil {
		t.Fatalf("count saga wallet operations: %v", err)
	}
	if operations != 0 {
		t.Fatalf("operation rows = %d, want 0 after insufficient funds", operations)
	}
}

func TestRepositoryCompensateDebitIsIdempotentAndRequiresDebit(t *testing.T) {
	ctx := context.Background()
	db := newIntegrationDB(t, ctx)
	from := createWalletFixture(t, ctx, db, uuid.New(), 5000)
	paymentID := uuid.New()

	_, err := db.CompensateDebit(ctx, domain.SagaCompensationCommand{
		PaymentID:      paymentID,
		FromWalletID:   from.ID,
		IdempotencyKey: "comp-key",
	})
	if err != domain.ErrDebitNotFound {
		t.Fatalf("CompensateDebit without debit error = %v, want %v", err, domain.ErrDebitNotFound)
	}

	debit, err := db.DebitForSaga(ctx, domain.SagaDebitCommand{
		PaymentID:      paymentID,
		FromWalletID:   from.ID,
		AmountCents:    1250,
		Currency:       "USD",
		IdempotencyKey: "debit-key",
	})
	if err != nil {
		t.Fatalf("DebitForSaga: %v", err)
	}
	first, err := db.CompensateDebit(ctx, domain.SagaCompensationCommand{
		PaymentID:      paymentID,
		FromWalletID:   from.ID,
		WalletDebitID:  debit.ID,
		AmountCents:    1250,
		Currency:       "USD",
		IdempotencyKey: "comp-key",
	})
	if err != nil {
		t.Fatalf("CompensateDebit first: %v", err)
	}
	second, err := db.CompensateDebit(ctx, domain.SagaCompensationCommand{
		PaymentID:      paymentID,
		FromWalletID:   from.ID,
		WalletDebitID:  debit.ID,
		AmountCents:    1250,
		Currency:       "USD",
		IdempotencyKey: "comp-key",
	})
	if err != nil {
		t.Fatalf("CompensateDebit duplicate: %v", err)
	}
	if first.ID != second.ID || first.BalanceAfterCents != 5000 || second.BalanceAfterCents != 5000 {
		t.Fatalf("compensations = %+v/%+v, want same completed compensation with balance 5000", first, second)
	}
	fromAfter, err := db.GetWallet(ctx, from.ID)
	if err != nil {
		t.Fatalf("GetWallet: %v", err)
	}
	if fromAfter.Balance != 5000 {
		t.Fatalf("source balance = %d, want 5000", fromAfter.Balance)
	}
}

func TestRepositoryAppendTransferEntriesCreatesDebitAndCreditIdempotently(t *testing.T) {
	ctx := context.Background()
	db := newIntegrationDB(t, ctx)
	fromUserID := uuid.New()
	from := createWalletFixture(t, ctx, db, fromUserID, 5000)
	to := createWalletFixture(t, ctx, db, uuid.New(), 250)
	transfer := createTransferRowFixture(t, ctx, db, from.ID, to.ID, 1250)

	if err := db.SetWalletBalanceForTest(ctx, from.ID, 3750); err != nil {
		t.Fatalf("SetWalletBalanceForTest(from): %v", err)
	}
	if err := db.SetWalletBalanceForTest(ctx, to.ID, 1500); err != nil {
		t.Fatalf("SetWalletBalanceForTest(to): %v", err)
	}

	if processed, err := db.TransferProcessed(ctx, transfer.ID); err != nil || processed {
		t.Fatalf("TransferProcessed before append = %v, %v; want false, nil", processed, err)
	}
	if err := db.AppendTransferEntries(ctx, LedgerTransfer{
		TransferID:   transfer.ID,
		FromWalletID: from.ID,
		ToWalletID:   to.ID,
		AmountCents:  1250,
		Currency:     "USD",
	}); err != nil {
		t.Fatalf("AppendTransferEntries first: %v", err)
	}
	if err := db.AppendTransferEntries(ctx, LedgerTransfer{
		TransferID:   transfer.ID,
		FromWalletID: from.ID,
		ToWalletID:   to.ID,
		AmountCents:  1250,
		Currency:     "USD",
	}); err != nil {
		t.Fatalf("AppendTransferEntries duplicate: %v", err)
	}

	if processed, err := db.TransferProcessed(ctx, transfer.ID); err != nil || !processed {
		t.Fatalf("TransferProcessed after append = %v, %v; want true, nil", processed, err)
	}
	fromEntries, _, err := db.ListLedgerEntries(ctx, from.ID, LedgerCursor{}, 10)
	if err != nil {
		t.Fatalf("ListLedgerEntries(from): %v", err)
	}
	toEntries, _, err := db.ListLedgerEntries(ctx, to.ID, LedgerCursor{}, 10)
	if err != nil {
		t.Fatalf("ListLedgerEntries(to): %v", err)
	}
	if len(fromEntries) != 1 || fromEntries[0].Direction != "debit" || fromEntries[0].Amount != 1250 || fromEntries[0].BalanceAfter != 3750 {
		t.Fatalf("from ledger entries = %+v", fromEntries)
	}
	if len(toEntries) != 1 || toEntries[0].Direction != "credit" || toEntries[0].Amount != 1250 || toEntries[0].BalanceAfter != 1500 {
		t.Fatalf("to ledger entries = %+v", toEntries)
	}
}

func TestRepositoryInsufficientFundsRollsBack(t *testing.T) {
	ctx := context.Background()
	db := newIntegrationDB(t, ctx)
	userID := uuid.New()
	from := createWalletFixture(t, ctx, db, userID, 100)
	to := createWalletFixture(t, ctx, db, uuid.New(), 50)

	if _, err := db.CreateTransfer(ctx, userID, from.ID, to.ID, 101); err != domain.ErrInsufficientFunds {
		t.Fatalf("CreateTransfer error = %v, want %v", err, domain.ErrInsufficientFunds)
	}

	fromAfter, err := db.GetWallet(ctx, from.ID)
	if err != nil {
		t.Fatalf("GetWallet(from): %v", err)
	}
	toAfter, err := db.GetWallet(ctx, to.ID)
	if err != nil {
		t.Fatalf("GetWallet(to): %v", err)
	}
	if fromAfter.Balance != 100 || toAfter.Balance != 50 {
		t.Fatalf("balances changed after rollback: %d/%d", fromAfter.Balance, toAfter.Balance)
	}
	entries, _, err := db.ListLedgerEntries(ctx, from.ID, LedgerCursor{}, 10)
	if err != nil {
		t.Fatalf("ListLedgerEntries: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("ledger entries were created after rollback: %+v", entries)
	}
	outboxEvents, err := outbox.NewRepository(db.pool).ClaimUnpublished(ctx, 10)
	if err != nil {
		t.Fatalf("ClaimUnpublished: %v", err)
	}
	if len(outboxEvents) != 0 {
		t.Fatalf("outbox events were created after rollback: %+v", outboxEvents)
	}
}

func TestRepositoryTransferRejectsUnownedSourceWallet(t *testing.T) {
	ctx := context.Background()
	db := newIntegrationDB(t, ctx)
	ownerID := uuid.New()
	from := createWalletFixture(t, ctx, db, ownerID, 1000)
	to := createWalletFixture(t, ctx, db, uuid.New(), 100)

	if _, err := db.CreateTransfer(ctx, uuid.New(), from.ID, to.ID, 100); err != domain.ErrNotFound {
		t.Fatalf("CreateTransfer error = %v, want %v", err, domain.ErrNotFound)
	}

	fromAfter, err := db.GetWallet(ctx, from.ID)
	if err != nil {
		t.Fatalf("GetWallet(from): %v", err)
	}
	toAfter, err := db.GetWallet(ctx, to.ID)
	if err != nil {
		t.Fatalf("GetWallet(to): %v", err)
	}
	if fromAfter.Balance != 1000 || toAfter.Balance != 100 {
		t.Fatalf("balances changed for unowned source: %d/%d", fromAfter.Balance, toAfter.Balance)
	}
}

func TestRepositoryTransferRejectsMissingDestinationWallet(t *testing.T) {
	ctx := context.Background()
	db := newIntegrationDB(t, ctx)
	userID := uuid.New()
	from := createWalletFixture(t, ctx, db, userID, 1000)

	if _, err := db.CreateTransfer(ctx, userID, from.ID, uuid.New(), 100); err != domain.ErrNotFound {
		t.Fatalf("CreateTransfer error = %v, want %v", err, domain.ErrNotFound)
	}

	fromAfter, err := db.GetWallet(ctx, from.ID)
	if err != nil {
		t.Fatalf("GetWallet(from): %v", err)
	}
	if fromAfter.Balance != 1000 {
		t.Fatalf("source balance changed for missing destination: %d", fromAfter.Balance)
	}
}

func TestRepositoryTransferRejectsCurrencyMismatch(t *testing.T) {
	ctx := context.Background()
	db := newIntegrationDB(t, ctx)
	userID := uuid.New()
	from := createWalletFixture(t, ctx, db, userID, 1000)
	to := createWalletFixture(t, ctx, db, uuid.New(), 100)
	setWalletCurrencyForTest(t, ctx, db, to.ID, "EUR")

	if _, err := db.CreateTransfer(ctx, userID, from.ID, to.ID, 100); err != domain.ErrCurrencyMismatch {
		t.Fatalf("CreateTransfer error = %v, want %v", err, domain.ErrCurrencyMismatch)
	}

	fromAfter, err := db.GetWallet(ctx, from.ID)
	if err != nil {
		t.Fatalf("GetWallet(from): %v", err)
	}
	toAfter, err := db.GetWallet(ctx, to.ID)
	if err != nil {
		t.Fatalf("GetWallet(to): %v", err)
	}
	if fromAfter.Balance != 1000 || toAfter.Balance != 100 {
		t.Fatalf("balances changed for currency mismatch: %d/%d", fromAfter.Balance, toAfter.Balance)
	}
}

func TestRepositoryLedgerPaginationIsStableNewestFirst(t *testing.T) {
	ctx := context.Background()
	db := newIntegrationDB(t, ctx)
	userID := uuid.New()
	from := createWalletFixture(t, ctx, db, userID, 1000)
	to := createWalletFixture(t, ctx, db, uuid.New(), 0)
	for range 3 {
		transfer, err := db.CreateTransfer(ctx, userID, from.ID, to.ID, 100)
		if err != nil {
			t.Fatalf("CreateTransfer: %v", err)
		}
		if err := db.AppendTransferEntries(ctx, LedgerTransfer{
			TransferID:   transfer.ID,
			FromWalletID: from.ID,
			ToWalletID:   to.ID,
			AmountCents:  100,
			Currency:     "USD",
		}); err != nil {
			t.Fatalf("AppendTransferEntries: %v", err)
		}
	}

	firstPage, next, err := db.ListLedgerEntries(ctx, from.ID, LedgerCursor{}, 2)
	if err != nil {
		t.Fatalf("ListLedgerEntries(first): %v", err)
	}
	if len(firstPage) != 2 {
		t.Fatalf("first page len = %d, want 2", len(firstPage))
	}
	if !next.Valid {
		t.Fatal("next cursor missing")
	}
	if firstPage[0].CreatedAt.Before(firstPage[1].CreatedAt) {
		t.Fatalf("entries are not newest first: %+v", firstPage)
	}

	secondPage, _, err := db.ListLedgerEntries(ctx, from.ID, next, 2)
	if err != nil {
		t.Fatalf("ListLedgerEntries(second): %v", err)
	}
	if len(secondPage) != 1 {
		t.Fatalf("second page len = %d, want 1", len(secondPage))
	}
	if secondPage[0].ID == firstPage[0].ID || secondPage[0].ID == firstPage[1].ID {
		t.Fatalf("pagination returned duplicate entry: %+v", secondPage[0])
	}
}

func TestRepositoryLedgerFiltersByWalletID(t *testing.T) {
	ctx := context.Background()
	db := newIntegrationDB(t, ctx)
	userID := uuid.New()
	from := createWalletFixture(t, ctx, db, userID, 1000)
	to := createWalletFixture(t, ctx, db, uuid.New(), 0)

	transfer, err := db.CreateTransfer(ctx, userID, from.ID, to.ID, 250)
	if err != nil {
		t.Fatalf("CreateTransfer: %v", err)
	}
	if err := db.AppendTransferEntries(ctx, LedgerTransfer{
		TransferID:   transfer.ID,
		FromWalletID: from.ID,
		ToWalletID:   to.ID,
		AmountCents:  250,
		Currency:     "USD",
	}); err != nil {
		t.Fatalf("AppendTransferEntries: %v", err)
	}

	fromEntries, _, err := db.ListLedgerEntries(ctx, from.ID, LedgerCursor{}, 10)
	if err != nil {
		t.Fatalf("ListLedgerEntries(from): %v", err)
	}
	toEntries, _, err := db.ListLedgerEntries(ctx, to.ID, LedgerCursor{}, 10)
	if err != nil {
		t.Fatalf("ListLedgerEntries(to): %v", err)
	}
	if len(fromEntries) != 1 || len(toEntries) != 1 {
		t.Fatalf("entry counts = %d/%d, want 1/1", len(fromEntries), len(toEntries))
	}
	if fromEntries[0].WalletID != from.ID || fromEntries[0].Direction != "debit" {
		t.Fatalf("from entries not filtered to source wallet: %+v", fromEntries)
	}
	if toEntries[0].WalletID != to.ID || toEntries[0].Direction != "credit" {
		t.Fatalf("to entries not filtered to destination wallet: %+v", toEntries)
	}
	if fromEntries[0].TransferID != transfer.ID || toEntries[0].TransferID != transfer.ID {
		t.Fatalf("transfer IDs = %s/%s, want %s", fromEntries[0].TransferID, toEntries[0].TransferID, transfer.ID)
	}
}

func TestRepositoryConcurrentTransfersNeverMakeSourceNegative(t *testing.T) {
	ctx := context.Background()
	db := newIntegrationDB(t, ctx)
	userID := uuid.New()
	from := createWalletFixture(t, ctx, db, userID, 100)
	to := createWalletFixture(t, ctx, db, uuid.New(), 0)

	results := make(chan error, 3)
	for range 3 {
		go func() {
			_, err := db.CreateTransfer(ctx, userID, from.ID, to.ID, 60)
			results <- err
		}()
	}

	successes := 0
	for range 3 {
		err := <-results
		if err == nil {
			successes++
			continue
		}
		if err != domain.ErrInsufficientFunds {
			t.Fatalf("CreateTransfer concurrent error = %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("successful transfers = %d, want 1", successes)
	}

	fromAfter, err := db.GetWallet(ctx, from.ID)
	if err != nil {
		t.Fatalf("GetWallet(from): %v", err)
	}
	if fromAfter.Balance < 0 {
		t.Fatalf("source balance is negative: %d", fromAfter.Balance)
	}
	if fromAfter.Balance != 40 {
		t.Fatalf("source balance = %d, want 40", fromAfter.Balance)
	}
}

func newIntegrationDB(t *testing.T, ctx context.Context) *Database {
	t.Helper()

	if databaseURL := os.Getenv("DATABASE_URL"); databaseURL != "" {
		db, err := Connect(ctx, databaseURL, 4)
		if err != nil {
			t.Fatalf("connect using DATABASE_URL: %v", err)
		}
		t.Cleanup(db.Close)
		if err := db.RunMigrations(ctx); err != nil {
			t.Fatalf("run migrations: %v", err)
		}
		if err := db.Truncate(ctx); err != nil {
			t.Fatalf("truncate test database: %v", err)
		}
		return db
	}

	req := testcontainers.ContainerRequest{
		Image:        "postgres:16-alpine",
		ExposedPorts: []string{"5432/tcp"},
		Env: map[string]string{
			"POSTGRES_DB":       "enjoythings_test",
			"POSTGRES_USER":     "enjoythings",
			"POSTGRES_PASSWORD": "enjoythings_test_password",
		},
		WaitingFor: wait.ForListeningPort("5432/tcp").WithStartupTimeout(60 * time.Second),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		t.Skipf("Postgres container unavailable: %v", err)
	}
	t.Cleanup(func() {
		if err := container.Terminate(context.Background()); err != nil {
			t.Logf("terminate Postgres container: %v", err)
		}
	})

	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("container host: %v", err)
	}

	port, err := container.MappedPort(ctx, "5432/tcp")
	if err != nil {
		t.Fatalf("container port: %v", err)
	}

	databaseURL := fmt.Sprintf(
		"postgres://enjoythings:enjoythings_test_password@%s:%s/enjoythings_test?sslmode=disable",
		host,
		port.Port(),
	)
	db, err := Connect(ctx, databaseURL, 2)
	if err != nil {
		t.Fatalf("connect to Postgres container: %v", err)
	}
	t.Cleanup(db.Close)

	if err := db.RunMigrations(ctx); err != nil {
		t.Fatalf("run migrations: %v", err)
	}
	return db
}

func createWalletFixture(t *testing.T, ctx context.Context, db *Database, userID uuid.UUID, balance int64) domain.Wallet {
	t.Helper()

	wallet, err := db.CreateWallet(ctx, userID, "USD")
	if err != nil {
		t.Fatalf("CreateWallet fixture: %v", err)
	}
	if balance == 0 {
		return wallet
	}
	if err := db.SetWalletBalanceForTest(ctx, wallet.ID, balance); err != nil {
		t.Fatalf("SetWalletBalanceForTest: %v", err)
	}
	wallet.Balance = balance
	return wallet
}

func createTransferRowFixture(t *testing.T, ctx context.Context, db *Database, fromWalletID, toWalletID uuid.UUID, amount int64) domain.Transfer {
	t.Helper()

	row, err := db.queries.CreateTransfer(ctx, queries.CreateTransferParams{
		FromWalletID: pgUUID(fromWalletID),
		ToWalletID:   pgUUID(toWalletID),
		Amount:       amount,
	})
	if err != nil {
		t.Fatalf("CreateTransfer row fixture: %v", err)
	}
	return transferFromQuery(row)
}

func setWalletCurrencyForTest(t *testing.T, ctx context.Context, db *Database, walletID uuid.UUID, currency string) {
	t.Helper()

	if _, err := db.pool.Exec(ctx, `UPDATE wallets SET currency = $2 WHERE id = $1`, pgUUID(walletID), currency); err != nil {
		t.Fatalf("set wallet currency fixture: %v", err)
	}
}
