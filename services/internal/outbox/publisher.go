package outbox

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
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
	Produce(ctx context.Context, topic string, key, value []byte) error
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
		if err := publisher.producer.Produce(ctx, event.Topic, []byte(event.PartitionKey), event.Payload); err != nil {
			publisher.logger.Error(
				"outbox publish failed",
				"outbox_id", event.ID,
				"topic", event.Topic,
				"partition_key", event.PartitionKey,
				"error", err,
			)
			return published, err
		}
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
