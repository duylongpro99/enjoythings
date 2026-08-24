package notification

import (
	"context"
	"errors"
	"log/slog"

	"enjoythings/services/internal/deadletter"
	"enjoythings/services/internal/telemetry"

	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel/trace"
)

type App interface {
	Dispatch(context.Context, Event) error
}

type Committer interface {
	CommitRecord(context.Context, *kgo.Record) error
}

type Consumer struct {
	app         App
	committer   Committer
	deadLetters deadletter.Publisher
	logger      *slog.Logger
}

// NewConsumer builds the record handler. A nil deadLetters publisher keeps
// poison records out of Kafka entirely, which is only appropriate in-process;
// the Kafka consumer below always wires one.
func NewConsumer(app App, committer Committer, deadLetters deadletter.Publisher, logger *slog.Logger) *Consumer {
	if logger == nil {
		logger = slog.Default()
	}
	return &Consumer{app: app, committer: committer, deadLetters: deadLetters, logger: logger}
}

// deadLetter parks a poison record and only then commits its offset, so a
// failed dead-letter write retries the record instead of losing it.
func (consumer *Consumer) deadLetter(ctx context.Context, record *kgo.Record, cause error) error {
	if consumer.deadLetters == nil {
		consumer.logger.Warn("notification dropped invalid event without a dead-letter publisher", "topic", record.Topic, "offset", record.Offset, "error", cause)
		return consumer.commit(ctx, record)
	}
	if err := consumer.deadLetters.Publish(ctx, deadletter.FromKafka(record, cause)); err != nil {
		consumer.logger.Error("notification dead-letter publish failed", "topic", record.Topic, "offset", record.Offset, "error", err)
		return err
	}
	telemetry.ServiceMetrics("notification").RecordKafka(deadletter.TopicFor(record.Topic), "produced")
	consumer.logger.Warn("notification dead-lettered invalid event", "topic", record.Topic, "offset", record.Offset, "error", cause)
	return consumer.commit(ctx, record)
}

func (consumer *Consumer) HandleRecord(ctx context.Context, record *kgo.Record) (err error) {
	defer func() {
		outcome := "consumed"
		if err != nil {
			outcome = "failed"
		}
		telemetry.ServiceMetrics("notification").RecordKafka(record.Topic, outcome)
	}()
	ctx = telemetry.ExtractKafka(ctx, record)
	ctx, span := telemetry.Tracer().Start(ctx, "kafka.consume", trace.WithSpanKind(trace.SpanKindConsumer), trace.WithAttributes(telemetry.SafeAttributes("messaging.kafka.topic", record.Topic, "operation", "consume")...))
	defer span.End()
	if !IsSupportedTopic(record.Topic) {
		consumer.logger.Warn("notification skipped unknown topic", "topic", record.Topic)
		return consumer.commit(ctx, record)
	}
	if err := consumer.app.Dispatch(ctx, Event{Topic: record.Topic, Payload: record.Value}); err != nil {
		if errors.Is(err, ErrInvalidEvent) {
			return consumer.deadLetter(ctx, record, err)
		}
		telemetry.RecordError(span, err)
		return err
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
		groupID = "notification-service"
	}
	if logger == nil {
		logger = slog.Default()
	}
	client, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		kgo.ConsumerGroup(groupID),
		kgo.ConsumeTopics(TopicTxCompleted, TopicTxFailed, TopicTxPaused, TopicUserVerified, TopicUserRejected),
		kgo.DisableAutoCommit(),
	)
	if err != nil {
		return nil, err
	}
	runner := &KafkaConsumer{client: client, logger: logger}
	runner.handler = NewConsumer(app, runner, deadletter.NewKafkaPublisher(client), logger)
	return runner, nil
}

func (consumer *KafkaConsumer) Run(ctx context.Context) {
	for {
		fetches := consumer.client.PollFetches(ctx)
		if ctx.Err() != nil {
			return
		}
		for _, err := range fetches.Errors() {
			consumer.logger.Error("notification poll failed", "topic", err.Topic, "partition", err.Partition, "error", err.Err)
		}
		fetches.EachRecord(func(record *kgo.Record) {
			if ctx.Err() != nil {
				return
			}
			if err := consumer.handler.HandleRecord(ctx, record); err != nil && ctx.Err() == nil {
				telemetry.ServiceMetrics("notification").RecordKafka(record.Topic, "retried")
				consumer.logger.Error("notification record failed", "topic", record.Topic, "partition", record.Partition, "offset", record.Offset, "error", err)
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
