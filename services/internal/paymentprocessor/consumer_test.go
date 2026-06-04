package paymentprocessor

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/twmb/franz-go/pkg/kgo"
)

func TestConsumerCommitsOffsetOnlyAfterProcessorSucceeds(t *testing.T) {
	processor := &fakeProcessor{}
	committer := &fakeCommitter{}
	consumer := NewConsumer(processor, committer, slog.New(slog.DiscardHandler))

	err := consumer.HandleRecord(context.Background(), &kgo.Record{
		Topic: PaymentExecuteTopic,
		Value: []byte(`{
			"event_id":"payment.execute:payment-1",
			"payment_id":"payment-1",
			"idempotency_key":"payment-1:execute-payment",
			"amount_cents":1250,
			"currency":"USD",
			"occurred_at":"2026-06-04T00:00:00Z"
		}`),
	})
	if err != nil {
		t.Fatalf("HandleRecord: %v", err)
	}
	if processor.calls != 1 {
		t.Fatalf("processor calls = %d, want 1", processor.calls)
	}
	if committer.count != 1 {
		t.Fatalf("commits = %d, want 1", committer.count)
	}
}

func TestConsumerDoesNotCommitWhenProcessorFails(t *testing.T) {
	processor := &fakeProcessor{err: errors.New("processor failed")}
	committer := &fakeCommitter{}
	consumer := NewConsumer(processor, committer, slog.New(slog.DiscardHandler))

	err := consumer.HandleRecord(context.Background(), &kgo.Record{
		Topic: PaymentExecuteTopic,
		Value: []byte(`{
			"event_id":"payment.execute:payment-1",
			"payment_id":"payment-1",
			"idempotency_key":"payment-1:execute-payment",
			"amount_cents":1250,
			"currency":"USD",
			"occurred_at":"2026-06-04T00:00:00Z"
		}`),
	})
	if err == nil {
		t.Fatal("expected processor error")
	}
	if committer.count != 0 {
		t.Fatalf("commits = %d, want 0", committer.count)
	}
}

func TestConsumerSkipsInvalidCommandAndCommitsOffset(t *testing.T) {
	processor := &fakeProcessor{}
	committer := &fakeCommitter{}
	consumer := NewConsumer(processor, committer, slog.New(slog.DiscardHandler))

	if err := consumer.HandleRecord(context.Background(), &kgo.Record{Topic: PaymentExecuteTopic, Value: []byte(`{`)}); err != nil {
		t.Fatalf("HandleRecord: %v", err)
	}
	if processor.calls != 0 {
		t.Fatalf("processor calls = %d, want 0", processor.calls)
	}
	if committer.count != 1 {
		t.Fatalf("commits = %d, want 1", committer.count)
	}
}

type fakeProcessor struct {
	calls int
	err   error
}

func (processor *fakeProcessor) HandleExecute(context.Context, PaymentExecute) error {
	processor.calls++
	return processor.err
}

type fakeCommitter struct {
	count int
}

func (committer *fakeCommitter) CommitRecord(context.Context, *kgo.Record) error {
	committer.count++
	return nil
}
