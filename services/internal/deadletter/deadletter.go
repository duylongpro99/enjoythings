// Package deadletter routes poison Kafka records to a per-topic dead-letter
// topic.
//
// Consumers previously logged a malformed payload and committed its offset,
// which made the record unrecoverable the moment the log line rotated away.
// Publishing the raw bytes plus the decode error keeps the evidence, and doing
// it before the commit means a broker outage retries the record instead of
// dropping it.
package deadletter

import (
	"context"
	"encoding/json"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
)

// Suffix is appended to a source topic to name its dead-letter topic.
const Suffix = ".dlq"

// SchemaVersion is carried in every dead-letter payload so consumers of the
// dead-letter topics can evolve with it.
const SchemaVersion = 1

// TopicFor names the dead-letter topic for a source topic.
func TopicFor(topic string) string {
	return topic + Suffix
}

// Record is a poison message and the reason it could not be handled.
type Record struct {
	Topic     string
	Partition int32
	Offset    int64
	Key       []byte
	Value     []byte
	Reason    string
	FailedAt  time.Time
}

// Publisher writes poison records to their dead-letter topic.
type Publisher interface {
	Publish(context.Context, Record) error
}

// Payload is the JSON written to the dead-letter topic. Key and value stay raw
// bytes (base64 in JSON) because a poison record is not necessarily valid JSON,
// or even valid UTF-8.
type Payload struct {
	SchemaVersion int       `json:"schema_version"`
	Topic         string    `json:"topic"`
	Partition     int32     `json:"partition"`
	Offset        int64     `json:"offset"`
	Key           []byte    `json:"key,omitempty"`
	Value         []byte    `json:"value"`
	Error         string    `json:"error"`
	FailedAt      time.Time `json:"failed_at"`
}

// KafkaPublisher publishes through an existing kgo client. Consumers reuse
// their own client: the dead-letter write has to land before the offset commit,
// so it belongs on the same connection lifetime.
type KafkaPublisher struct {
	client *kgo.Client
	now    func() time.Time
}

func NewKafkaPublisher(client *kgo.Client) *KafkaPublisher {
	return &KafkaPublisher{client: client, now: time.Now}
}

func (publisher *KafkaPublisher) Publish(ctx context.Context, record Record) error {
	payload, err := Encode(record, publisher.now())
	if err != nil {
		return err
	}
	return publisher.client.ProduceSync(ctx, &kgo.Record{
		Topic: TopicFor(record.Topic),
		Key:   record.Key,
		Value: payload,
	}).FirstErr()
}

// Encode renders the dead-letter payload, defaulting the failure time.
func Encode(record Record, fallbackFailedAt time.Time) ([]byte, error) {
	failedAt := record.FailedAt
	if failedAt.IsZero() {
		failedAt = fallbackFailedAt
	}
	return json.Marshal(Payload{
		SchemaVersion: SchemaVersion,
		Topic:         record.Topic,
		Partition:     record.Partition,
		Offset:        record.Offset,
		Key:           record.Key,
		Value:         record.Value,
		Error:         record.Reason,
		FailedAt:      failedAt.UTC(),
	})
}

// FromKafka builds a Record from the consumed record and its decode error.
func FromKafka(record *kgo.Record, cause error) Record {
	reason := ""
	if cause != nil {
		reason = cause.Error()
	}
	return Record{
		Topic:     record.Topic,
		Partition: record.Partition,
		Offset:    record.Offset,
		Key:       record.Key,
		Value:     record.Value,
		Reason:    reason,
	}
}
