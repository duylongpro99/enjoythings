package ledgerconsumer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"enjoythings/services/internal/domain"
	"enjoythings/services/internal/event"
	"enjoythings/services/internal/repo"

	"github.com/google/uuid"
	"github.com/twmb/franz-go/pkg/kgo"
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
	store     Store
	committer Committer
	logger    *slog.Logger
}

func New(store Store, committer Committer, logger *slog.Logger) *Consumer {
	if logger == nil {
		logger = slog.Default()
	}
	return &Consumer{store: store, committer: committer, logger: logger}
}

func (consumer *Consumer) HandleRecord(ctx context.Context, record *kgo.Record) error {
	transfer, err := decodeTransfer(record.Value)
	if err != nil {
		consumer.logger.Warn("ledger consumer skipped invalid tx.initiated event", "topic", record.Topic, "partition", record.Partition, "offset", record.Offset, "error", err)
		return consumer.commit(ctx, record)
	}

	if err := consumer.store.AppendTransferEntries(ctx, transfer); err != nil {
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
	runner.handler = New(store, runner, logger)
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
