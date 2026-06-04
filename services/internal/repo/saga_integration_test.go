package repo

import (
	"context"
	"errors"
	"testing"

	"enjoythings/services/internal/saga"

	"github.com/google/uuid"
)

func TestSagaStoreCreatesReadsAndUpdatesSaga(t *testing.T) {
	ctx := context.Background()
	db := newIntegrationDB(t, ctx)
	store := db.SagaStore()
	paymentID := uuid.NewString()

	created, err := store.Create(ctx, saga.Saga{
		PaymentID:      paymentID,
		IdempotencyKey: "idem-1",
		UserID:         uuid.NewString(),
		FromWalletID:   uuid.NewString(),
		ToWalletID:     uuid.NewString(),
		AmountCents:    1250,
		Currency:       "USD",
		State:          saga.StateStarted,
		TraceID:        "trace-1",
	})
	if err != nil {
		t.Fatalf("Create saga: %v", err)
	}
	if created.ID == "" {
		t.Fatal("saga ID was not assigned")
	}

	created.State = saga.StatePaymentProcessing
	created.WalletDebitID = "wallet-debit-1"
	created.LedgerReservationID = "ledger-reservation-1"
	updated, err := store.Update(ctx, created)
	if err != nil {
		t.Fatalf("Update saga: %v", err)
	}

	got, err := store.GetByPaymentID(ctx, paymentID)
	if err != nil {
		t.Fatalf("GetByPaymentID: %v", err)
	}
	if got.State != saga.StatePaymentProcessing || got.WalletDebitID != "wallet-debit-1" || got.LedgerReservationID != "ledger-reservation-1" {
		t.Fatalf("got saga = %+v, want updated %+v", got, updated)
	}
}

func TestSagaStoreDuplicateIdempotencyReturnsExistingAndConflictFails(t *testing.T) {
	ctx := context.Background()
	db := newIntegrationDB(t, ctx)
	store := db.SagaStore()
	userID := uuid.NewString()
	first := saga.Saga{
		PaymentID:      uuid.NewString(),
		IdempotencyKey: "idem-1",
		UserID:         userID,
		FromWalletID:   uuid.NewString(),
		ToWalletID:     uuid.NewString(),
		AmountCents:    1250,
		Currency:       "USD",
		State:          saga.StateStarted,
	}

	created, err := store.Create(ctx, first)
	if err != nil {
		t.Fatalf("Create first saga: %v", err)
	}
	duplicate, err := store.Create(ctx, first)
	if err != nil {
		t.Fatalf("Create duplicate saga: %v", err)
	}
	if duplicate.ID != created.ID {
		t.Fatalf("duplicate ID = %s, want existing %s", duplicate.ID, created.ID)
	}

	first.AmountCents++
	_, err = store.Create(ctx, first)
	if !errors.Is(err, saga.ErrAlreadyExists) {
		t.Fatalf("conflicting duplicate error = %v, want %v", err, saga.ErrAlreadyExists)
	}
}

func TestSagaStoreListsOnlyNonTerminalSagas(t *testing.T) {
	ctx := context.Background()
	db := newIntegrationDB(t, ctx)
	store := db.SagaStore()

	processing, err := store.Create(ctx, sagaFixture(saga.StatePaymentProcessing))
	if err != nil {
		t.Fatalf("Create processing saga: %v", err)
	}
	if _, err := store.Create(ctx, sagaFixture(saga.StateCompleted)); err != nil {
		t.Fatalf("Create completed saga: %v", err)
	}
	if _, err := store.Create(ctx, sagaFixture(saga.StateFailed)); err != nil {
		t.Fatalf("Create failed saga: %v", err)
	}

	got, err := store.ListNonTerminal(ctx)
	if err != nil {
		t.Fatalf("ListNonTerminal: %v", err)
	}
	if len(got) != 1 || got[0].PaymentID != processing.PaymentID {
		t.Fatalf("non-terminal sagas = %+v, want only %s", got, processing.PaymentID)
	}
}

func sagaFixture(state string) saga.Saga {
	return saga.Saga{
		PaymentID:      uuid.NewString(),
		IdempotencyKey: uuid.NewString(),
		UserID:         uuid.NewString(),
		FromWalletID:   uuid.NewString(),
		ToWalletID:     uuid.NewString(),
		AmountCents:    1250,
		Currency:       "USD",
		State:          state,
	}
}
