package sagaconsumer

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"enjoythings/services/internal/deadletter"
	"enjoythings/services/internal/event"
	"enjoythings/services/internal/saga"

	"github.com/twmb/franz-go/pkg/kgo"
)

func TestConsumerHandlesFraudFlagged(t *testing.T) {
	app := &fakeApp{}
	committer := &fakeCommitter{}
	deadLetters := &fakeDeadLetters{}
	consumer := New(app, committer, deadLetters, nil)
	payload, _ := json.Marshal(event.FraudFlagged{
		SchemaVersion: 1,
		EventID:       "fraud.flagged:source",
		SourceEventID: "source",
		PaymentID:     "payment-1",
		SessionID:     "session-1",
		Action:        event.FraudActionFlag,
		RiskScore:     0.8,
		Reason:        "velocity",
		ProviderID:    "provider-1",
		ModelID:       "model-1",
		OccurredAt:    time.Now().UTC(),
		TraceID:       "trace-1",
	})

	err := consumer.HandleRecord(context.Background(), &kgo.Record{
		Topic: event.FraudFlaggedTopic,
		Value: payload,
	})
	if err != nil {
		t.Fatal(err)
	}
	if app.fraud.PaymentID != "payment-1" {
		t.Fatalf("fraud event = %+v", app.fraud)
	}
	if committer.calls != 1 {
		t.Fatalf("commits = %d, want 1", committer.calls)
	}
}

func TestConsumerDeadLettersInvalidFraudEventBeforeCommitting(t *testing.T) {
	app := &fakeApp{}
	committer := &fakeCommitter{}
	deadLetters := &fakeDeadLetters{}
	consumer := New(app, committer, deadLetters, nil)

	err := consumer.HandleRecord(context.Background(), &kgo.Record{
		Topic:  event.FraudFlaggedTopic,
		Offset: 9,
		Value:  []byte(`{"schema_version":1,"payment_id":"payment-1"}`),
	})
	if err != nil {
		t.Fatalf("HandleRecord: %v", err)
	}
	if app.fraud.PaymentID != "" {
		t.Fatalf("app saw %+v, want no dispatch", app.fraud)
	}
	if len(deadLetters.records) != 1 || deadLetters.records[0].Topic != event.FraudFlaggedTopic {
		t.Fatalf("dead letters = %+v, want the fraud event parked", deadLetters.records)
	}
	if committer.calls != 1 {
		t.Fatalf("commits = %d, want 1", committer.calls)
	}
}

func TestConsumerDeadLettersPaymentResultMissingPaymentID(t *testing.T) {
	app := &fakeApp{}
	committer := &fakeCommitter{}
	deadLetters := &fakeDeadLetters{}
	consumer := New(app, committer, deadLetters, nil)

	err := consumer.HandleRecord(context.Background(), &kgo.Record{
		Topic: PaymentCompletedTopic,
		Value: []byte(`{"event_id":"payment.completed:x"}`),
	})
	if err != nil {
		t.Fatalf("HandleRecord: %v", err)
	}
	if len(deadLetters.records) != 1 || deadLetters.records[0].Reason == "" {
		t.Fatalf("dead letters = %+v, want one parked record with a reason", deadLetters.records)
	}
	if committer.calls != 1 {
		t.Fatalf("commits = %d, want 1", committer.calls)
	}
}

func TestConsumerKeepsOffsetWhenDeadLetterPublishFails(t *testing.T) {
	committer := &fakeCommitter{}
	deadLetters := &fakeDeadLetters{err: errors.New("broker unavailable")}
	consumer := New(&fakeApp{}, committer, deadLetters, nil)

	err := consumer.HandleRecord(context.Background(), &kgo.Record{
		Topic: PaymentFailedTopic,
		Value: []byte(`not json`),
	})
	if err == nil {
		t.Fatal("expected the dead-letter failure to surface")
	}
	if committer.calls != 0 {
		t.Fatalf("commits = %d, want 0 so the record is retried", committer.calls)
	}
}

type fakeDeadLetters struct {
	records []deadletter.Record
	err     error
}

func (publisher *fakeDeadLetters) Publish(_ context.Context, record deadletter.Record) error {
	if publisher.err != nil {
		return publisher.err
	}
	publisher.records = append(publisher.records, record)
	return nil
}

type fakeApp struct {
	fraud event.FraudFlagged
}

func (app *fakeApp) HandlePaymentCompleted(context.Context, saga.PaymentCompleted) error {
	return nil
}

func (app *fakeApp) HandlePaymentFailed(context.Context, saga.PaymentFailed) error {
	return nil
}

func (app *fakeApp) HandleFraudFlagged(_ context.Context, flagged event.FraudFlagged) error {
	app.fraud = flagged
	return nil
}

type fakeCommitter struct {
	calls int
}

func (committer *fakeCommitter) CommitRecord(context.Context, *kgo.Record) error {
	committer.calls++
	return nil
}
