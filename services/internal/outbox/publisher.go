package outbox

import (
	"context"
	"log/slog"
	"time"

	"enjoythings/services/internal/telemetry"

	"github.com/google/uuid"
	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

const (
	defaultPollInterval = 100 * time.Millisecond
	defaultBatchSize    = 100
)

type Store interface {
	ClaimUnpublished(context.Context, int) ([]Event, error)
	MarkPublished(context.Context, uuid.UUID) error
}

type Producer interface {
	Produce(ctx context.Context, topic string, key, value []byte, headers []kgo.RecordHeader) error
}

type PublisherConfig struct {
	PollInterval time.Duration
	BatchSize    int
}

type Publisher struct {
	store    Store
	producer Producer
	cfg      PublisherConfig
	logger   *slog.Logger
}

func NewPublisher(store Store, producer Producer, cfg PublisherConfig, logger *slog.Logger) *Publisher {
	if cfg.PollInterval <= 0 {
		cfg.PollInterval = defaultPollInterval
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = defaultBatchSize
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Publisher{store: store, producer: producer, cfg: cfg, logger: logger}
}

func (publisher *Publisher) Run(ctx context.Context) {
	ticker := time.NewTicker(publisher.cfg.PollInterval)
	defer ticker.Stop()

	for {
		if _, err := publisher.PublishBatch(ctx); err != nil && ctx.Err() == nil {
			publisher.logger.Error("outbox publish batch failed", "error", err)
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (publisher *Publisher) PublishBatch(ctx context.Context) (int, error) {
	events, err := publisher.store.ClaimUnpublished(ctx, publisher.cfg.BatchSize)
	if err != nil {
		return 0, err
	}

	published := 0
	for _, event := range events {
		eventCtx := event.Context(ctx)
		eventCtx, span := telemetry.Tracer().Start(
			eventCtx,
			"kafka.produce",
			trace.WithSpanKind(trace.SpanKindProducer),
			trace.WithAttributes(telemetry.SafeAttributes(
				"messaging.kafka.topic", event.Topic,
				"operation", "produce",
			)...),
		)
		headers := event.Headers()
		record := &kgo.Record{Headers: headers}
		telemetry.InjectKafka(eventCtx, record)
		err := publisher.producer.Produce(eventCtx, event.Topic, []byte(event.PartitionKey), event.Payload, record.Headers)
		if err != nil {
			telemetry.ServiceMetrics("outbox").RecordKafka(event.Topic, "failed")
			telemetry.RecordError(span, err)
			publisher.logger.Error(
				"outbox publish failed",
				"outbox_id", event.ID,
				"topic", event.Topic,
				"partition_key", event.PartitionKey,
				"error", err,
			)
			span.End()
			return published, err
		}
		telemetry.ServiceMetrics("outbox").RecordKafka(event.Topic, "produced")
		span.End()
		if err := publisher.store.MarkPublished(ctx, event.ID); err != nil {
			publisher.logger.Error(
				"outbox mark published failed",
				"outbox_id", event.ID,
				"topic", event.Topic,
				"partition_key", event.PartitionKey,
				"error", err,
			)
			return published, err
		}
		published++
	}
	return published, nil
}

func (event Event) Context(ctx context.Context) context.Context {
	carrier := propagation.MapCarrier{}
	if event.Traceparent != "" {
		carrier.Set(telemetry.TraceparentHeader, event.Traceparent)
	}
	if event.Tracestate != "" {
		carrier.Set(telemetry.TracestateHeader, event.Tracestate)
	}
	return telemetry.ExtractTextMap(ctx, carrier)
}

func (event Event) Headers() []kgo.RecordHeader {
	headers := make([]kgo.RecordHeader, 0, 2)
	if event.Traceparent != "" {
		headers = append(headers, kgo.RecordHeader{Key: telemetry.TraceparentHeader, Value: []byte(event.Traceparent)})
	}
	if event.Tracestate != "" {
		headers = append(headers, kgo.RecordHeader{Key: telemetry.TracestateHeader, Value: []byte(event.Tracestate)})
	}
	return headers
}
