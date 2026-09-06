package deadletter

import (
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
)

func TestDecodeRoundTripsAnEncodedRecord(t *testing.T) {
	failedAt := time.Date(2026, 6, 4, 12, 0, 0, 0, time.UTC)
	encoded, err := Encode(Record{
		Topic:     "payment.completed",
		Partition: 2,
		Offset:    17,
		Key:       []byte("payment-9"),
		Value:     []byte{0xff, '{'},
		Reason:    "unexpected end of JSON input",
	}, failedAt)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	payload, err := Decode(encoded)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if payload.Topic != "payment.completed" || payload.Partition != 2 || payload.Offset != 17 {
		t.Fatalf("payload coordinates = %s/%d/%d", payload.Topic, payload.Partition, payload.Offset)
	}
	if string(payload.Key) != "payment-9" || string(payload.Value) != string([]byte{0xff, '{'}) {
		t.Fatalf("payload key/value = %q/%v, want the original bytes", payload.Key, payload.Value)
	}
	if payload.Error != "unexpected end of JSON input" || !payload.FailedAt.Equal(failedAt) {
		t.Fatalf("payload error/failed_at = %q/%s", payload.Error, payload.FailedAt)
	}
}

func TestDecodeRejectsUnusablePayloads(t *testing.T) {
	for name, value := range map[string]string{
		"not json":       "nope",
		"future schema":  `{"schema_version":2,"topic":"tx.failed","value":"e30="}`,
		"missing topic":  `{"schema_version":1,"value":"e30="}`,
		"empty document": `{}`,
	} {
		if _, err := Decode([]byte(value)); err == nil {
			t.Fatalf("%s: Decode accepted %q", name, value)
		}
	}
}

func TestReplayTargetsTheSourceTopicWithTheOriginalBytes(t *testing.T) {
	parked := &kgo.Record{Topic: "tx.initiated.dlq", Partition: 0, Offset: 5}
	payload := Payload{Topic: "tx.initiated", Key: []byte("payment-1"), Value: []byte("original")}

	replayed := Replay(parked, payload, nil)

	if replayed.Topic != "tx.initiated" || string(replayed.Key) != "payment-1" || string(replayed.Value) != "original" {
		t.Fatalf("replayed = %s %q %q, want the source topic with the original key and value", replayed.Topic, replayed.Key, replayed.Value)
	}
	if len(replayed.Headers) != 1 || replayed.Headers[0].Key != RedriveHeader || string(replayed.Headers[0].Value) != "tx.initiated.dlq:0:5" {
		t.Fatalf("headers = %+v, want %s naming the dead-letter coordinates", replayed.Headers, RedriveHeader)
	}
}

func TestReplayUsesTheCorrectedValueWhenGiven(t *testing.T) {
	parked := &kgo.Record{Topic: "fraud.flagged.dlq", Offset: 1}
	payload := Payload{Topic: "fraud.flagged", Key: []byte("payment-2"), Value: []byte("broken")}

	replayed := Replay(parked, payload, []byte(`{"fixed":true}`))

	if string(replayed.Value) != `{"fixed":true}` {
		t.Fatalf("value = %q, want the override", replayed.Value)
	}
	if string(replayed.Key) != "payment-2" {
		t.Fatalf("key = %q, want the producer's key kept so partitioning is unchanged", replayed.Key)
	}
}
