package deadletter

import (
	"encoding/json"
	"fmt"

	"github.com/twmb/franz-go/pkg/kgo"
)

// RedriveHeader is set on every replayed record and names the dead-letter
// coordinates it came from, so a consumer or an investigator can tell a
// redriven record from the producer's original.
const RedriveHeader = "x-dead-letter-redrive"

// Decode parses a dead-letter payload. A schema version this build does not
// know is an error rather than a best-effort read: replaying a record whose
// layout is misunderstood would put the wrong bytes on the source topic.
func Decode(value []byte) (Payload, error) {
	var payload Payload
	if err := json.Unmarshal(value, &payload); err != nil {
		return Payload{}, fmt.Errorf("decode dead-letter payload: %w", err)
	}
	if payload.SchemaVersion != SchemaVersion {
		return Payload{}, fmt.Errorf("dead-letter schema version %d is not supported (want %d)", payload.SchemaVersion, SchemaVersion)
	}
	if payload.Topic == "" {
		return Payload{}, fmt.Errorf("dead-letter payload has no source topic")
	}
	return payload, nil
}

// Replay builds the record that puts a parked message back on its source
// topic. parked is the record read from the dead-letter topic; a non-nil
// override replaces the poison value with the operator's corrected bytes while
// the key, and therefore the partition, stays the producer's.
func Replay(parked *kgo.Record, payload Payload, override []byte) *kgo.Record {
	value := payload.Value
	if override != nil {
		value = override
	}
	return &kgo.Record{
		Topic: payload.Topic,
		Key:   payload.Key,
		Value: value,
		Headers: []kgo.RecordHeader{{
			Key:   RedriveHeader,
			Value: []byte(fmt.Sprintf("%s:%d:%d", parked.Topic, parked.Partition, parked.Offset)),
		}},
	}
}
