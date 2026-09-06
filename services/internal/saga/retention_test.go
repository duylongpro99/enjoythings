package saga

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestFraudAuditSweeperDeletesExpiredRowsOfTerminalAndOrphanSagasOnly(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	old := now.Add(-48 * time.Hour)
	store := newMemoryStore()
	for paymentID, state := range map[string]string{
		"completed":  StateCompleted,
		"failed":     StateFailed,
		"review":     StateFraudReview,
		"processing": StatePaymentProcessing,
	} {
		if _, err := store.Create(ctx, Saga{PaymentID: paymentID, IdempotencyKey: paymentID, UserID: "user", State: state}); err != nil {
			t.Fatalf("create %s saga: %v", paymentID, err)
		}
	}
	for _, audit := range []FraudAuditRecord{
		{EventID: "old-completed", PaymentID: "completed", Kind: FraudAuditKindTransition, CreatedAt: old},
		{EventID: "old-failed", PaymentID: "failed", Kind: FraudAuditKindTransition, CreatedAt: old},
		{EventID: "old-orphan", PaymentID: "missing", Kind: FraudAuditKindOrphan, CreatedAt: old},
		{EventID: "old-review", PaymentID: "review", Kind: FraudAuditKindDeferredTerminal, CreatedAt: old},
		{EventID: "old-processing", PaymentID: "processing", Kind: FraudAuditKindIgnored, CreatedAt: old},
		{EventID: "fresh-completed", PaymentID: "completed", Kind: FraudAuditKindTransition, CreatedAt: now.Add(-time.Hour)},
	} {
		if err := store.RecordFraudAudit(ctx, audit); err != nil {
			t.Fatalf("record audit %s: %v", audit.EventID, err)
		}
	}

	sweeper := NewFraudAuditSweeper(store, FraudAuditSweeperConfig{Retention: 24 * time.Hour}, clockAt{now}, nil)
	deleted, err := sweeper.Sweep(ctx)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if deleted != 3 {
		t.Fatalf("deleted = %d, want 3", deleted)
	}
	for _, eventID := range []string{"old-completed", "old-failed", "old-orphan"} {
		if _, ok := store.audits[eventID]; ok {
			t.Fatalf("expired audit %s for a terminal or missing saga was kept", eventID)
		}
	}
	for _, eventID := range []string{"old-review", "old-processing", "fresh-completed"} {
		if _, ok := store.audits[eventID]; !ok {
			t.Fatalf("audit %s must be kept: non-terminal saga or inside the window", eventID)
		}
	}
}

func TestFraudAuditSweeperDrainsBacklogInBoundedBatches(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	store := &countingRetentionStore{memoryStore: newMemoryStore()}
	for index := range 25 {
		audit := FraudAuditRecord{
			EventID:   fmt.Sprintf("orphan-%02d", index),
			PaymentID: "missing",
			Kind:      FraudAuditKindOrphan,
			CreatedAt: now.Add(-time.Duration(index+1) * 24 * time.Hour),
		}
		if err := store.RecordFraudAudit(ctx, audit); err != nil {
			t.Fatalf("record audit: %v", err)
		}
	}

	sweeper := NewFraudAuditSweeper(store, FraudAuditSweeperConfig{Retention: time.Hour, BatchSize: 10}, clockAt{now}, nil)
	deleted, err := sweeper.Sweep(ctx)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if deleted != 25 {
		t.Fatalf("deleted = %d, want 25", deleted)
	}
	if store.calls != 3 {
		t.Fatalf("delete batches = %d, want 3 (10 + 10 + 5)", store.calls)
	}
	if len(store.audits) != 0 {
		t.Fatalf("audits remaining = %d, want 0", len(store.audits))
	}
}

func TestFraudAuditSweeperKeepsEverythingWhenRetentionIsUnset(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	store := &countingRetentionStore{memoryStore: newMemoryStore()}
	if err := store.RecordFraudAudit(ctx, FraudAuditRecord{EventID: "ancient", PaymentID: "missing", Kind: FraudAuditKindOrphan, CreatedAt: now.AddDate(-10, 0, 0)}); err != nil {
		t.Fatalf("record audit: %v", err)
	}

	sweeper := NewFraudAuditSweeper(store, FraudAuditSweeperConfig{}, clockAt{now}, nil)
	deleted, err := sweeper.Sweep(ctx)
	if err != nil {
		t.Fatalf("Sweep: %v", err)
	}
	if deleted != 0 || store.calls != 0 {
		t.Fatalf("deleted = %d, calls = %d; zero retention must not touch the store", deleted, store.calls)
	}
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	sweeper.Run(ctx)
	if store.calls != 0 {
		t.Fatal("Run must return immediately when retention is unset")
	}
}

func TestFraudAuditSweeperReturnsStoreErrors(t *testing.T) {
	wantErr := errors.New("database unavailable")
	sweeper := NewFraudAuditSweeper(failingRetentionStore{err: wantErr}, FraudAuditSweeperConfig{Retention: time.Hour}, nil, nil)
	if _, err := sweeper.Sweep(context.Background()); !errors.Is(err, wantErr) {
		t.Fatalf("Sweep error = %v, want %v", err, wantErr)
	}
}

type countingRetentionStore struct {
	*memoryStore
	calls int
}

func (store *countingRetentionStore) DeleteFraudAuditBefore(ctx context.Context, before time.Time, limit int) (int64, error) {
	store.calls++
	return store.memoryStore.DeleteFraudAuditBefore(ctx, before, limit)
}

type failingRetentionStore struct{ err error }

func (store failingRetentionStore) DeleteFraudAuditBefore(context.Context, time.Time, int) (int64, error) {
	return 0, store.err
}
