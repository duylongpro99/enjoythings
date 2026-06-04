package repo

import (
	"context"
	"testing"
	"time"

	"enjoythings/services/internal/paymentprocessor"
	"enjoythings/services/internal/saga"
)

func TestPaymentAttemptStorePersistsStatusAcrossStoreInstances(t *testing.T) {
	ctx := context.Background()
	db := newIntegrationDB(t, ctx)
	firstStore := db.PaymentAttemptStore()
	command := saga.PaymentExecute{
		PaymentID:           "payment-persist-1",
		IdempotencyKey:      "payment-persist-1:execute-payment",
		TraceID:             "trace-1",
		AmountCents:         1250,
		Currency:            "USD",
		LedgerReservationID: "ledger-reservation-1",
		WalletDebitID:       "wallet-debit-1",
		OccurredAt:          time.Date(2026, 6, 4, 0, 0, 0, 0, time.UTC),
	}

	created, err := firstStore.CreatePending(ctx, command, command.OccurredAt)
	if err != nil {
		t.Fatalf("CreatePending: %v", err)
	}
	if created.Status != paymentprocessor.StatusPending {
		t.Fatalf("created status = %s, want PENDING", created.Status)
	}
	if _, err := firstStore.MarkCompleted(ctx, command.PaymentID, paymentprocessor.RailResult{
		ProcessorPaymentID: "rail-payment-1",
		CompletedAt:        time.Date(2026, 6, 4, 1, 0, 0, 0, time.UTC),
	}, time.Date(2026, 6, 4, 1, 0, 1, 0, time.UTC)); err != nil {
		t.Fatalf("MarkCompleted: %v", err)
	}

	secondStore := db.PaymentAttemptStore()
	got, err := secondStore.GetByPaymentID(ctx, command.PaymentID)
	if err != nil {
		t.Fatalf("GetByPaymentID after new store: %v", err)
	}
	if got.Status != paymentprocessor.StatusCompleted || got.ProcessorPaymentID != "rail-payment-1" {
		t.Fatalf("persisted attempt = %+v, want completed rail-payment-1", got)
	}
}
