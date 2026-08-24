package deadletter

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
)

func TestTopicForAppendsSuffix(t *testing.T) {
	if got := TopicFor("payment.completed"); got != "payment.completed.dlq" {
		t.Fatalf("TopicFor = %q, want payment.completed.dlq", got)
	}
}

func TestEncodeKeepsRawBytesAndFailureReason(t *testing.T) {
	failedAt := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	// Deliberately not valid UTF-8: a poison record is arbitrary bytes.
	value := []byte{0xff, 0xfe, '{'}

	encoded, err := Encode(Record{
		Topic:     "tx.initiated",
		Partition: 3,
		Offset:    42,
		Key:       []byte("payment-1"),
		Value:     value,
		Reason:    "decode json: unexpected end of JSON input",
	}, failedAt)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	var payload Payload
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload.SchemaVersion != SchemaVersion || payload.Topic != "tx.initiated" || payload.Partition != 3 || payload.Offset != 42 {
		t.Fatalf("payload = %+v", payload)
	}
	if string(payload.Key) != "payment-1" || string(payload.Value) != string(value) {
		t.Fatalf("payload key/value = %q/%v, want the original bytes", payload.Key, payload.Value)
	}
	if payload.Error == "" || !payload.FailedAt.Equal(failedAt) {
		t.Fatalf("payload error/failed_at = %q/%s", payload.Error, payload.FailedAt)
	}
}

func TestEncodePrefersTheRecordFailureTime(t *testing.T) {
	recorded := time.Date(2026, 6, 4, 9, 0, 0, 0, time.UTC)
	fallback := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)

	encoded, err := Encode(Record{Topic: "tx.failed", Value: []byte("{}"), FailedAt: recorded}, fallback)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	var payload Payload
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if !payload.FailedAt.Equal(recorded) {
		t.Fatalf("failed_at = %s, want %s", payload.FailedAt, recorded)
	}
}

func TestFromKafkaCarriesCoordinatesAndCause(t *testing.T) {
	record := &kgo.Record{
		Topic:     "fraud.flagged",
		Partition: 1,
		Offset:    7,
		Key:       []byte("payment-2"),
		Value:     []byte("not json"),
	}

	got := FromKafka(record, errors.New("invalid fraud event"))

	if got.Topic != record.Topic || got.Partition != record.Partition || got.Offset != record.Offset {
		t.Fatalf("record = %+v, want the kafka coordinates", got)
	}
	if got.Reason != "invalid fraud event" || string(got.Value) != "not json" {
		t.Fatalf("record = %+v, want the cause and raw value", got)
	}
	if !got.FailedAt.IsZero() {
		t.Fatalf("failed_at = %s, want zero so the publisher stamps it", got.FailedAt)
	}
}
