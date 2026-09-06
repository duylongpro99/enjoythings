package repo

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"enjoythings/services/internal/outbox"
	"enjoythings/services/internal/saga"

	"github.com/google/uuid"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
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

func TestSagaStoreAtomicallyUpdatesStateAndCreatesOutboxEvents(t *testing.T) {
	ctx := context.Background()
	db := newIntegrationDB(t, ctx)
	// The orchestrator always writes these rows inside a span; the outbox row
	// carries that context to the relay and on to the fraud worker.
	ctx, span := sdktrace.NewTracerProvider().Tracer("test").Start(ctx, "saga.outbox")
	defer span.End()
	store := db.SagaStore()
	created, err := store.Create(ctx, sagaFixture(saga.StateLedgerReserved))
	if err != nil {
		t.Fatalf("Create saga: %v", err)
	}
	created.State = saga.StatePaymentProcessing

	updated, err := store.UpdateWithOutbox(ctx, created, []saga.OutboxRecord{
		{Topic: saga.TopicPaymentExecute, PartitionKey: created.PaymentID, Payload: []byte(`{"kind":"payment"}`)},
		{Topic: "fraud.score.requested", PartitionKey: created.PaymentID, Payload: []byte(`{"kind":"fraud"}`)},
	})
	if err != nil {
		t.Fatalf("UpdateWithOutbox: %v", err)
	}
	if updated.State != saga.StatePaymentProcessing {
		t.Fatalf("state = %s, want %s", updated.State, saga.StatePaymentProcessing)
	}
	events, err := outbox.NewRepository(db.pool).ClaimUnpublished(ctx, 10)
	if err != nil {
		t.Fatalf("ClaimUnpublished: %v", err)
	}
	if len(events) != 2 || events[0].Topic != saga.TopicPaymentExecute || events[1].Topic != "fraud.score.requested" {
		t.Fatalf("events = %+v, want payment and fraud events", events)
	}
	for _, event := range events {
		if event.Traceparent == "" {
			t.Fatalf("%s outbox row has no traceparent; the fraud worker cannot continue the saga trace", event.Topic)
		}
	}
}

func TestSagaStoreRollsBackOutboxWhenAtomicStateUpdateFails(t *testing.T) {
	ctx := context.Background()
	db := newIntegrationDB(t, ctx)
	store := db.SagaStore()

	_, err := store.UpdateWithOutbox(ctx, saga.Saga{
		PaymentID: uuid.NewString(),
		State:     saga.StatePaymentProcessing,
	}, []saga.OutboxRecord{
		{Topic: saga.TopicPaymentExecute, PartitionKey: "missing-payment", Payload: []byte(`{}`)},
	})
	if !errors.Is(err, saga.ErrNotFound) {
		t.Fatalf("UpdateWithOutbox error = %v, want %v", err, saga.ErrNotFound)
	}
	events, err := outbox.NewRepository(db.pool).ClaimUnpublished(ctx, 10)
	if err != nil {
		t.Fatalf("ClaimUnpublished: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("events = %+v, want transaction rollback", events)
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

func TestSagaStoreRecordsFraudAuditRecords(t *testing.T) {
	ctx := context.Background()
	db := newIntegrationDB(t, ctx)
	store := db.SagaStore()

	audit := saga.FraudAuditRecord{
		EventID:     "fraud.flagged:source",
		PaymentID:   uuid.NewString(),
		Kind:        saga.FraudAuditKindOrphan,
		SagaState:   "",
		DetailsJSON: `{"kind":"orphan"}`,
	}
	if err := store.RecordFraudAudit(ctx, audit); err != nil {
		t.Fatalf("RecordFraudAudit: %v", err)
	}

	var gotEventID, gotPaymentID, gotKind, gotState, gotDetails string
	if err := db.pool.QueryRow(ctx, `
SELECT event_id, payment_id, kind, saga_state, details_json
FROM saga_fraud_audit_records
WHERE event_id = $1`, audit.EventID).Scan(&gotEventID, &gotPaymentID, &gotKind, &gotState, &gotDetails); err != nil {
		t.Fatalf("query fraud audit: %v", err)
	}
	if gotEventID != audit.EventID || gotPaymentID != audit.PaymentID || gotKind != audit.Kind || gotState != audit.SagaState || gotDetails != audit.DetailsJSON {
		t.Fatalf("fraud audit row = %s/%s/%s/%s/%s, want %+v", gotEventID, gotPaymentID, gotKind, gotState, gotDetails, audit)
	}
}

func TestSagaStoreListsFraudReviewQueueAndAuditTrail(t *testing.T) {
	ctx := context.Background()
	db := newIntegrationDB(t, ctx)
	store := db.SagaStore()

	flaggedAt := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	newer := sagaFixture(saga.StateFraudReview)
	newer.FraudFlaggedAt = flaggedAt
	older := sagaFixture(saga.StateFraudReview)
	older.FraudFlaggedAt = flaggedAt.Add(-time.Hour)
	for _, fixture := range []saga.Saga{newer, older, sagaFixture(saga.StatePaymentProcessing)} {
		if _, err := store.Create(ctx, fixture); err != nil {
			t.Fatalf("Create %s saga: %v", fixture.State, err)
		}
	}

	queue, err := store.ListFraudReview(ctx)
	if err != nil {
		t.Fatalf("ListFraudReview: %v", err)
	}
	if len(queue) != 2 || queue[0].PaymentID != older.PaymentID || queue[1].PaymentID != newer.PaymentID {
		t.Fatalf("queue = %+v, want [%s %s] longest-held first", queue, older.PaymentID, newer.PaymentID)
	}

	kinds := []string{saga.FraudAuditKindTransition, saga.FraudAuditKindDeferredTerminal, saga.FraudAuditKindReviewResumed}
	for i, kind := range kinds {
		if err := store.RecordFraudAudit(ctx, saga.FraudAuditRecord{
			EventID:     fmt.Sprintf("%s:%d", newer.PaymentID, i),
			PaymentID:   newer.PaymentID,
			Kind:        kind,
			SagaState:   saga.StateFraudReview,
			DetailsJSON: `{}`,
			CreatedAt:   flaggedAt.Add(time.Duration(i) * time.Minute),
		}); err != nil {
			t.Fatalf("RecordFraudAudit %s: %v", kind, err)
		}
	}
	if err := store.RecordFraudAudit(ctx, saga.FraudAuditRecord{
		EventID: older.PaymentID + ":0", PaymentID: older.PaymentID, Kind: saga.FraudAuditKindTransition, CreatedAt: flaggedAt,
	}); err != nil {
		t.Fatalf("RecordFraudAudit for other payment: %v", err)
	}

	trail, err := store.ListFraudAudit(ctx, newer.PaymentID)
	if err != nil {
		t.Fatalf("ListFraudAudit: %v", err)
	}
	if len(trail) != len(kinds) {
		t.Fatalf("trail = %+v, want %d records for the payment only", trail, len(kinds))
	}
	for i, record := range trail {
		if record.Kind != kinds[i] || record.PaymentID != newer.PaymentID || !record.CreatedAt.Equal(flaggedAt.Add(time.Duration(i)*time.Minute)) {
			t.Fatalf("trail[%d] = %+v, want %s in creation order", i, record, kinds[i])
		}
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
