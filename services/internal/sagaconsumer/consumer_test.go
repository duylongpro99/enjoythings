package sagaconsumer

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"enjoythings/services/internal/event"
	"enjoythings/services/internal/saga"

	"github.com/twmb/franz-go/pkg/kgo"
)

func TestConsumerHandlesFraudFlagged(t *testing.T) {
	app := &fakeApp{}
	committer := &fakeCommitter{}
	consumer := New(app, committer, nil)
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
