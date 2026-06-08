package paymentprocessor

import (
	"context"
	"encoding/json"
	"log/slog"

	"enjoythings/services/internal/saga"
	"enjoythings/services/internal/telemetry"

	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel/trace"
)

type PaymentExecute = saga.PaymentExecute

type App interface {
	HandleExecute(context.Context, PaymentExecute) error
}

type Committer interface {
	CommitRecord(context.Context, *kgo.Record) error
}

type Consumer struct {
	app       App
	committer Committer
	logger    *slog.Logger
}

func NewConsumer(app App, committer Committer, logger *slog.Logger) *Consumer {
	if logger == nil {
		logger = slog.Default()
	}
	return &Consumer{app: app, committer: committer, logger: logger}
}

func (consumer *Consumer) HandleRecord(ctx context.Context, record *kgo.Record) error {
	ctx = telemetry.ExtractKafka(ctx, record)
	ctx, span := telemetry.Tracer().Start(ctx, "kafka.consume", trace.WithSpanKind(trace.SpanKindConsumer), trace.WithAttributes(telemetry.SafeAttributes("messaging.kafka.topic", record.Topic, "operation", "consume")...))
	defer span.End()
	if record.Topic != PaymentExecuteTopic {
		consumer.logger.Warn("payment processor skipped unknown topic", "topic", record.Topic)
		return consumer.commit(ctx, record)
	}
	var command PaymentExecute
	if err := json.Unmarshal(record.Value, &command); err != nil || validateCommand(command) != nil {
		consumer.logger.Warn("payment processor skipped invalid payment.execute command", "error", err)
		return consumer.commit(ctx, record)
	}
	if err := consumer.app.HandleExecute(ctx, command); err != nil {
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
		groupID = "payment-processor"
	}
	if logger == nil {
		logger = slog.Default()
	}
	client, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		kgo.ConsumerGroup(groupID),
		kgo.ConsumeTopics(PaymentExecuteTopic),
		kgo.DisableAutoCommit(),
	)
	if err != nil {
		return nil, err
	}
	runner := &KafkaConsumer{client: client, logger: logger}
	runner.handler = NewConsumer(app, runner, logger)
	return runner, nil
}

func (consumer *KafkaConsumer) Run(ctx context.Context) {
	for {
		fetches := consumer.client.PollFetches(ctx)
		if ctx.Err() != nil {
			return
		}
		for _, err := range fetches.Errors() {
			consumer.logger.Error("payment processor poll failed", "topic", err.Topic, "partition", err.Partition, "error", err.Err)
		}
		fetches.EachRecord(func(record *kgo.Record) {
			if ctx.Err() != nil {
				return
			}
			if err := consumer.handler.HandleRecord(ctx, record); err != nil && ctx.Err() == nil {
				consumer.logger.Error("payment processor record failed", "topic", record.Topic, "partition", record.Partition, "offset", record.Offset, "error", err)
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
