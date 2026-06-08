package outbox

import (
	"context"

	"github.com/twmb/franz-go/pkg/kgo"
)

type KafkaProducer struct {
	client *kgo.Client
}

func NewKafkaProducer(brokers []string) (*KafkaProducer, error) {
	client, err := kgo.NewClient(kgo.SeedBrokers(brokers...))
	if err != nil {
		return nil, err
	}
	return &KafkaProducer{client: client}, nil
}

func (producer *KafkaProducer) Produce(ctx context.Context, topic string, key, value []byte, headers []kgo.RecordHeader) error {
	record := &kgo.Record{
		Topic:   topic,
		Key:     key,
		Value:   value,
		Headers: headers,
	}
	return producer.client.ProduceSync(ctx, record).FirstErr()
}

func (producer *KafkaProducer) Close() {
	producer.client.Close()
}
