package ledgerconsumer

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/twmb/franz-go/pkg/kgo"
)

func TestHandleRecordAppendsValidTransferAndCommitsOffset(t *testing.T) {
	store := &fakeStore{}
	committer := &fakeCommitter{}
	consumer := New(store, committer, slog.New(slog.DiscardHandler))
	transferID := uuid.New()
	fromWalletID := uuid.New()
	toWalletID := uuid.New()

	record := &kgo.Record{Value: []byte(`{
		"transfer_id":"` + transferID.String() + `",
		"from_wallet_id":"` + fromWalletID.String() + `",
		"to_wallet_id":"` + toWalletID.String() + `",
		"amount_cents":1250,
		"currency":"USD",
		"initiated_at":"2026-06-03T00:00:00Z"
	}`)}

	if err := consumer.HandleRecord(context.Background(), record); err != nil {
		t.Fatalf("HandleRecord: %v", err)
	}
	if len(store.transfers) != 1 {
		t.Fatalf("stored transfers = %d, want 1", len(store.transfers))
	}
	got := store.transfers[0]
	if got.TransferID != transferID || got.FromWalletID != fromWalletID || got.ToWalletID != toWalletID || got.AmountCents != 1250 || got.Currency != "USD" {
		t.Fatalf("stored transfer = %+v", got)
	}
	if committer.count != 1 {
		t.Fatalf("commits = %d, want 1", committer.count)
	}
}

func TestHandleRecordSkipsMalformedJSONAndCommitsOffset(t *testing.T) {
	store := &fakeStore{}
	committer := &fakeCommitter{}
	consumer := New(store, committer, slog.New(slog.DiscardHandler))

	if err := consumer.HandleRecord(context.Background(), &kgo.Record{Value: []byte(`{`)}); err != nil {
		t.Fatalf("HandleRecord malformed: %v", err)
	}
	if len(store.transfers) != 0 {
		t.Fatalf("stored transfers = %d, want 0", len(store.transfers))
	}
	if committer.count != 1 {
		t.Fatalf("commits = %d, want 1", committer.count)
	}
}

func TestHandleRecordRejectsSchemaInvalidEventAndCommitsOffset(t *testing.T) {
	store := &fakeStore{}
	committer := &fakeCommitter{}
	consumer := New(store, committer, slog.New(slog.DiscardHandler))

	err := consumer.HandleRecord(context.Background(), &kgo.Record{Value: []byte(`{
		"transfer_id":"not-a-uuid",
		"from_wallet_id":"` + uuid.NewString() + `",
		"to_wallet_id":"` + uuid.NewString() + `",
		"amount_cents":1250,
		"currency":"USD",
		"initiated_at":"2026-06-03T00:00:00Z"
	}`)})
	if err != nil {
		t.Fatalf("HandleRecord invalid schema: %v", err)
	}
	if len(store.transfers) != 0 {
		t.Fatalf("stored transfers = %d, want 0", len(store.transfers))
	}
	if committer.count != 1 {
		t.Fatalf("commits = %d, want 1", committer.count)
	}
}

func TestHandleRecordDoesNotCommitOffsetWhenStoreFails(t *testing.T) {
	store := &fakeStore{err: errors.New("database unavailable")}
	committer := &fakeCommitter{}
	consumer := New(store, committer, slog.New(slog.DiscardHandler))

	err := consumer.HandleRecord(context.Background(), &kgo.Record{Value: []byte(`{
		"transfer_id":"` + uuid.NewString() + `",
		"from_wallet_id":"` + uuid.NewString() + `",
		"to_wallet_id":"` + uuid.NewString() + `",
		"amount_cents":1250,
		"currency":"USD",
		"initiated_at":"2026-06-03T00:00:00Z"
	}`)})
	if err == nil {
		t.Fatal("expected store failure")
	}
	if committer.count != 0 {
		t.Fatalf("commits = %d, want 0", committer.count)
	}
}

type fakeStore struct {
	transfers []Transfer
	err       error
}

func (store *fakeStore) AppendTransferEntries(_ context.Context, transfer Transfer) error {
	if store.err != nil {
		return store.err
	}
	store.transfers = append(store.transfers, transfer)
	return nil
}

type fakeCommitter struct {
	count int
}

func (committer *fakeCommitter) CommitRecord(context.Context, *kgo.Record) error {
	committer.count++
	return nil
}
