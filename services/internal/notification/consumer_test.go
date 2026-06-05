package notification

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/twmb/franz-go/pkg/kgo"
)

func TestConsumerCommitsOffsetAfterDispatchSucceeds(t *testing.T) {
	app := &fakeDispatcher{}
	committer := &fakeCommitter{}
	consumer := NewConsumer(app, committer, slog.New(slog.DiscardHandler))

	err := consumer.HandleRecord(context.Background(), &kgo.Record{
		Topic: TopicTxCompleted,
		Value: []byte(`{"payment_id":"payment-1","trace_id":"trace-1","amount_cents":1250,"currency":"USD"}`),
	})
	if err != nil {
		t.Fatalf("HandleRecord: %v", err)
	}
	if app.calls != 1 {
		t.Fatalf("dispatch calls = %d, want 1", app.calls)
	}
	if committer.count != 1 {
		t.Fatalf("commits = %d, want 1", committer.count)
	}
}

func TestConsumerDoesNotCommitOffsetWhenDispatchFails(t *testing.T) {
	app := &fakeDispatcher{err: errors.New("adapter failed")}
	committer := &fakeCommitter{}
	consumer := NewConsumer(app, committer, slog.New(slog.DiscardHandler))

	err := consumer.HandleRecord(context.Background(), &kgo.Record{
		Topic: TopicUserRejected,
		Value: []byte(`{"user_id":"user-1","verification_id":"ver-1","trace_id":"trace-1","reason":"expired"}`),
	})
	if err == nil {
		t.Fatal("expected dispatch error")
	}
	if committer.count != 0 {
		t.Fatalf("commits = %d, want 0", committer.count)
	}
}

func TestConsumerCommitsInvalidPayloadWithoutDispatchingAdapters(t *testing.T) {
	email := &recordingAdapter{}
	sms := &recordingAdapter{}
	committer := &fakeCommitter{}
	dispatcher := NewDispatcher(email, sms, slog.New(slog.DiscardHandler))
	consumer := NewConsumer(dispatcher, committer, slog.New(slog.DiscardHandler))

	err := consumer.HandleRecord(context.Background(), &kgo.Record{Topic: TopicTxCompleted, Value: []byte(`{`)})
	if err != nil {
		t.Fatalf("HandleRecord: %v", err)
	}
	if len(email.messages) != 0 || len(sms.messages) != 0 {
		t.Fatalf("messages = email:%d sms:%d, want none", len(email.messages), len(sms.messages))
	}
	if committer.count != 1 {
		t.Fatalf("commits = %d, want 1", committer.count)
	}
}

func TestConsumerCommitsUnknownTopicWithoutDispatch(t *testing.T) {
	app := &fakeDispatcher{}
	committer := &fakeCommitter{}
	consumer := NewConsumer(app, committer, slog.New(slog.DiscardHandler))

	err := consumer.HandleRecord(context.Background(), &kgo.Record{Topic: "phase4.fraud.detected", Value: []byte(`{}`)})
	if err != nil {
		t.Fatalf("HandleRecord: %v", err)
	}
	if app.calls != 0 {
		t.Fatalf("dispatch calls = %d, want 0", app.calls)
	}
	if committer.count != 1 {
		t.Fatalf("commits = %d, want 1", committer.count)
	}
}

type fakeDispatcher struct {
	calls int
	err   error
}

func (dispatcher *fakeDispatcher) Dispatch(context.Context, Event) error {
	dispatcher.calls++
	return dispatcher.err
}

type fakeCommitter struct {
	count int
}

func (committer *fakeCommitter) CommitRecord(context.Context, *kgo.Record) error {
	committer.count++
	return nil
}
