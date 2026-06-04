package sagaconsumer

import (
	"context"
	"encoding/json"
	"log/slog"

	"enjoythings/services/internal/saga"

	"github.com/twmb/franz-go/pkg/kgo"
)

const (
	PaymentCompletedTopic = "payment.completed"
	PaymentFailedTopic    = "payment.failed"
	DefaultGroupID        = "saga-orchestrator"
)

type App interface {
	HandlePaymentCompleted(context.Context, saga.PaymentCompleted) error
	HandlePaymentFailed(context.Context, saga.PaymentFailed) error
}

type Committer interface {
	CommitRecord(context.Context, *kgo.Record) error
}

type Consumer struct {
	app       App
	committer Committer
	logger    *slog.Logger
}

func New(app App, committer Committer, logger *slog.Logger) *Consumer {
	if logger == nil {
		logger = slog.Default()
	}
	return &Consumer{app: app, committer: committer, logger: logger}
}

func (consumer *Consumer) HandleRecord(ctx context.Context, record *kgo.Record) error {
	switch record.Topic {
	case PaymentCompletedTopic:
		var event saga.PaymentCompleted
		if err := json.Unmarshal(record.Value, &event); err != nil || event.PaymentID == "" {
			consumer.logger.Warn("saga consumer skipped invalid payment.completed event", "error", err)
			return consumer.commit(ctx, record)
		}
		if err := consumer.app.HandlePaymentCompleted(ctx, event); err != nil {
			return err
		}
	case PaymentFailedTopic:
		var event saga.PaymentFailed
		if err := json.Unmarshal(record.Value, &event); err != nil || event.PaymentID == "" {
			consumer.logger.Warn("saga consumer skipped invalid payment.failed event", "error", err)
			return consumer.commit(ctx, record)
		}
		if err := consumer.app.HandlePaymentFailed(ctx, event); err != nil {
			return err
		}
	default:
		consumer.logger.Warn("saga consumer skipped unknown topic", "topic", record.Topic)
	}
	return consumer.commit(ctx, record)
}

func (consumer *Consumer) commit(ctx context.Context, record *kgo.Record) error {
	if consumer.committer == nil {
		return nil
	}
	return consumer.committer.CommitRecord(ctx, record)
}

type KafkaConsumer struct {
	client  *kgo.Client
	handler *Consumer
	logger  *slog.Logger
}

func NewKafkaConsumer(brokers []string, groupID string, app App, logger *slog.Logger) (*KafkaConsumer, error) {
	if groupID == "" {
		groupID = DefaultGroupID
	}
	if logger == nil {
		logger = slog.Default()
	}
	client, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		kgo.ConsumerGroup(groupID),
		kgo.ConsumeTopics(PaymentCompletedTopic, PaymentFailedTopic),
		kgo.DisableAutoCommit(),
	)
	if err != nil {
		return nil, err
	}
	runner := &KafkaConsumer{client: client, logger: logger}
	runner.handler = New(app, runner, logger)
	return runner, nil
}

func (consumer *KafkaConsumer) Run(ctx context.Context) {
	for {
		fetches := consumer.client.PollFetches(ctx)
		if ctx.Err() != nil {
			return
		}
		for _, err := range fetches.Errors() {
			consumer.logger.Error("saga consumer poll failed", "topic", err.Topic, "partition", err.Partition, "error", err.Err)
		}
		fetches.EachRecord(func(record *kgo.Record) {
			if ctx.Err() != nil {
				return
			}
			if err := consumer.handler.HandleRecord(ctx, record); err != nil && ctx.Err() == nil {
				consumer.logger.Error("saga consumer record failed", "topic", record.Topic, "partition", record.Partition, "offset", record.Offset, "error", err)
			}
		})
	}
}

func (consumer *KafkaConsumer) CommitRecord(ctx context.Context, record *kgo.Record) error {
	return consumer.client.CommitRecords(ctx, record)
}

func (consumer *KafkaConsumer) Close() {
	consumer.client.Close()
}
