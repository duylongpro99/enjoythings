package outbox

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"github.com/twmb/franz-go/pkg/kgo"
)

func TestPublisherMarksRowsPublishedAfterSuccessfulProduce(t *testing.T) {
	firstID := uuid.New()
	secondID := uuid.New()
	store := &fakeStore{events: []Event{
		{ID: firstID, Topic: "tx.initiated", PartitionKey: "wallet-1", Payload: []byte(`{"transfer_id":"one"}`)},
		{ID: secondID, Topic: "tx.initiated", PartitionKey: "wallet-2", Payload: []byte(`{"transfer_id":"two"}`)},
	}}
	producer := &fakeProducer{}
	publisher := NewPublisher(store, producer, PublisherConfig{BatchSize: 10}, slog.Default())

	published, err := publisher.PublishBatch(context.Background())
	if err != nil {
		t.Fatalf("PublishBatch: %v", err)
	}
	if published != 2 {
		t.Fatalf("published = %d, want 2", published)
	}
	if store.claimBatchSize != 10 {
		t.Fatalf("claim batch size = %d, want 10", store.claimBatchSize)
	}
	if len(store.marked) != 2 || store.marked[0] != firstID || store.marked[1] != secondID {
		t.Fatalf("marked IDs = %v, want [%s %s]", store.marked, firstID, secondID)
	}
	if len(producer.records) != 2 {
		t.Fatalf("produced records len = %d, want 2", len(producer.records))
	}
	if string(producer.records[0].key) != "wallet-1" || string(producer.records[0].value) != `{"transfer_id":"one"}` {
		t.Fatalf("first produced record = %+v", producer.records[0])
	}
}

func TestPublisherInjectsPersistedTraceContextIntoKafkaHeaders(t *testing.T) {
	eventID := uuid.New()
	store := &fakeStore{events: []Event{
		{
			ID:           eventID,
			Topic:        "fraud.score.requested",
			PartitionKey: "payment-1",
			Payload:      []byte(`{"trace_id":"compatibility-only"}`),
			Traceparent:  "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
		},
	}}
	producer := &fakeProducer{}
	publisher := NewPublisher(store, producer, PublisherConfig{BatchSize: 1}, slog.Default())

	if _, err := publisher.PublishBatch(context.Background()); err != nil {
		t.Fatalf("PublishBatch: %v", err)
	}

	traceparent := ""
	for _, header := range producer.records[0].headers {
		if header.Key == "traceparent" {
			traceparent = string(header.Value)
		}
	}
	if traceparent == "" || traceparent == "compatibility-only" {
		t.Fatalf("traceparent header = %q, want W3C context from outbox metadata", traceparent)
	}
}

func TestPublisherLeavesRowsUnpublishedAfterProduceFailure(t *testing.T) {
	eventID := uuid.New()
	store := &fakeStore{events: []Event{
		{ID: eventID, Topic: "tx.initiated", PartitionKey: "wallet-1", Payload: []byte(`{"transfer_id":"one"}`)},
	}}
	producer := &fakeProducer{err: errors.New("kafka unavailable")}
	publisher := NewPublisher(store, producer, PublisherConfig{BatchSize: 10}, slog.Default())

	published, err := publisher.PublishBatch(context.Background())
	if err == nil {
		t.Fatal("expected produce failure")
	}
	if published != 0 {
		t.Fatalf("published = %d, want 0", published)
	}
	if len(store.marked) != 0 {
		t.Fatalf("marked IDs = %v, want none", store.marked)
	}
}

type fakeStore struct {
	events         []Event
	claimBatchSize int
	marked         []uuid.UUID
}

func (store *fakeStore) ClaimUnpublished(_ context.Context, batchSize int) ([]Event, error) {
	store.claimBatchSize = batchSize
	return store.events, nil
}

func (store *fakeStore) MarkPublished(_ context.Context, id uuid.UUID) error {
	store.marked = append(store.marked, id)
	return nil
}

type fakeProducer struct {
	records []producedRecord
	err     error
}

func (producer *fakeProducer) Produce(_ context.Context, topic string, key, value []byte, headers []kgo.RecordHeader) error {
	if producer.err != nil {
		return producer.err
	}
	producer.records = append(producer.records, producedRecord{
		topic:   topic,
		key:     append([]byte(nil), key...),
		value:   append([]byte(nil), value...),
		headers: append([]kgo.RecordHeader(nil), headers...),
	})
	return nil
}

type producedRecord struct {
	topic   string
	key     []byte
	value   []byte
	headers []kgo.RecordHeader
}
