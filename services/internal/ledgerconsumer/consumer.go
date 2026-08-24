package ledgerconsumer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"enjoythings/services/internal/deadletter"
	"enjoythings/services/internal/domain"
	"enjoythings/services/internal/event"
	"enjoythings/services/internal/repo"
	"enjoythings/services/internal/telemetry"

	"github.com/google/uuid"
	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel/trace"
)

const (
	defaultGroupID = "ledger-service"
)

type Transfer = repo.LedgerTransfer

type Store interface {
	AppendTransferEntries(context.Context, Transfer) error
}

type Committer interface {
	CommitRecord(context.Context, *kgo.Record) error
}

type Consumer struct {
	store       Store
	committer   Committer
	deadLetters deadletter.Publisher
	logger      *slog.Logger
}

// New builds the record handler. A nil deadLetters publisher keeps poison
// records out of Kafka entirely, which is only appropriate in-process; the
// Kafka consumer below always wires one.
func New(store Store, committer Committer, deadLetters deadletter.Publisher, logger *slog.Logger) *Consumer {
	if logger == nil {
		logger = slog.Default()
	}
	return &Consumer{store: store, committer: committer, deadLetters: deadLetters, logger: logger}
}

// deadLetter parks a poison record and only then commits its offset, so a
// failed dead-letter write retries the record instead of losing it.
func (consumer *Consumer) deadLetter(ctx context.Context, record *kgo.Record, cause error) error {
	if consumer.deadLetters == nil {
		consumer.logger.Warn("ledger consumer dropped invalid event without a dead-letter publisher", "topic", record.Topic, "offset", record.Offset, "error", cause)
		return consumer.commit(ctx, record)
	}
	if err := consumer.deadLetters.Publish(ctx, deadletter.FromKafka(record, cause)); err != nil {
		consumer.logger.Error("ledger consumer dead-letter publish failed", "topic", record.Topic, "offset", record.Offset, "error", err)
		return err
	}
	telemetry.ServiceMetrics("ledger").RecordKafka(deadletter.TopicFor(record.Topic), "produced")
	consumer.logger.Warn("ledger consumer dead-lettered invalid event", "topic", record.Topic, "partition", record.Partition, "offset", record.Offset, "error", cause)
	return consumer.commit(ctx, record)
}

func (consumer *Consumer) HandleRecord(ctx context.Context, record *kgo.Record) (err error) {
	defer func() {
		outcome := "consumed"
		if err != nil {
			outcome = "failed"
		}
		telemetry.ServiceMetrics("ledger").RecordKafka(record.Topic, outcome)
	}()
	ctx = telemetry.ExtractKafka(ctx, record)
	ctx, span := telemetry.Tracer().Start(ctx, "kafka.consume", trace.WithSpanKind(trace.SpanKindConsumer), trace.WithAttributes(telemetry.SafeAttributes("messaging.kafka.topic", record.Topic, "operation", "consume")...))
	defer span.End()
	transfer, err := decodeTransfer(record.Value)
	if err != nil {
		return consumer.deadLetter(ctx, record, err)
	}

	if err := consumer.store.AppendTransferEntries(ctx, transfer); err != nil {
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

func NewKafkaConsumer(brokers []string, topic, groupID string, store Store, logger *slog.Logger) (*KafkaConsumer, error) {
	if topic == "" {
		topic = event.TransactionInitiatedTopic
	}
	if groupID == "" {
		groupID = defaultGroupID
	}
	if logger == nil {
		logger = slog.Default()
	}
	client, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		kgo.ConsumerGroup(groupID),
		kgo.ConsumeTopics(topic),
		kgo.DisableAutoCommit(),
	)
	if err != nil {
		return nil, err
	}
	runner := &KafkaConsumer{client: client, logger: logger}
	runner.handler = New(store, runner, deadletter.NewKafkaPublisher(client), logger)
	return runner, nil
}

func (consumer *KafkaConsumer) Run(ctx context.Context) {
	for {
		fetches := consumer.client.PollFetches(ctx)
		if ctx.Err() != nil {
			return
		}
		for _, err := range fetches.Errors() {
			consumer.logger.Error("ledger consumer poll failed", "topic", err.Topic, "partition", err.Partition, "error", err.Err)
		}
		fetches.EachRecord(func(record *kgo.Record) {
			if ctx.Err() != nil {
				return
			}
			if err := consumer.handler.HandleRecord(ctx, record); err != nil && ctx.Err() == nil {
				telemetry.ServiceMetrics("ledger").RecordKafka(record.Topic, "retried")
				consumer.logger.Error("ledger consumer record failed", "topic", record.Topic, "partition", record.Partition, "offset", record.Offset, "error", err)
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

func decodeTransfer(payload []byte) (Transfer, error) {
	var initiated event.TransactionInitiated
	if err := json.Unmarshal(payload, &initiated); err != nil {
		return Transfer{}, fmt.Errorf("decode json: %w", err)
	}
	transferID, err := parseRequiredUUID(initiated.TransferID, "transfer_id")
	if err != nil {
		return Transfer{}, err
	}
	fromWalletID, err := parseRequiredUUID(initiated.FromWalletID, "from_wallet_id")
	if err != nil {
		return Transfer{}, err
	}
	toWalletID, err := parseRequiredUUID(initiated.ToWalletID, "to_wallet_id")
	if err != nil {
		return Transfer{}, err
	}
	if fromWalletID == toWalletID {
		return Transfer{}, errors.New("from_wallet_id and to_wallet_id must differ")
	}
	if initiated.AmountCents <= 0 {
		return Transfer{}, errors.New("amount_cents must be positive")
	}
	currency, err := domain.NormalizeCurrency(initiated.Currency)
	if err != nil {
		return Transfer{}, err
	}
	if initiated.InitiatedAt.IsZero() {
		return Transfer{}, errors.New("initiated_at is required")
	}
	return Transfer{
		TransferID:   transferID,
		FromWalletID: fromWalletID,
		ToWalletID:   toWalletID,
		AmountCents:  initiated.AmountCents,
		Currency:     currency,
	}, nil
}

func parseRequiredUUID(raw string, field string) (uuid.UUID, error) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, fmt.Errorf("%s must be a valid UUID", field)
	}
	return id, nil
}
