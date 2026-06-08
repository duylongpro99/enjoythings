package outbox

import (
	"context"
	"time"

	"enjoythings/services/internal/repo/queries"
	"enjoythings/services/internal/telemetry"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

type Event struct {
	ID           uuid.UUID
	Topic        string
	PartitionKey string
	Payload      []byte
	Traceparent  string
	Tracestate   string
	ClaimedAt    *time.Time
	PublishedAt  *time.Time
	CreatedAt    time.Time
}

type Repository struct {
	queries *queries.Queries
}

func NewRepository(db queries.DBTX) *Repository {
	return &Repository{queries: queries.New(db)}
}

func (repo *Repository) Enqueue(ctx context.Context, topic, partitionKey string, payload []byte) (Event, error) {
	carrier := propagation.MapCarrier{}
	telemetry.InjectTextMap(ctx, carrier)
	ctx, span := telemetry.Tracer().Start(
		ctx,
		"outbox.enqueue",
		trace.WithSpanKind(trace.SpanKindInternal),
		trace.WithAttributes(telemetry.SafeAttributes(
			"messaging.kafka.topic", topic,
			"operation", "enqueue",
		)...),
	)
	defer span.End()
	row, err := repo.queries.CreateOutboxEvent(ctx, queries.CreateOutboxEventParams{
		Topic:        topic,
		PartitionKey: partitionKey,
		Payload:      payload,
		Traceparent:  carrier.Get(telemetry.TraceparentHeader),
		Tracestate:   carrier.Get(telemetry.TracestateHeader),
	})
	if err != nil {
		telemetry.RecordError(span, err)
		return Event{}, err
	}
	return eventFromQuery(row), nil
}

func (repo *Repository) ClaimUnpublished(ctx context.Context, batchSize int) ([]Event, error) {
	if batchSize < 1 {
		batchSize = 1
	}
	rows, err := repo.queries.ClaimUnpublishedOutboxEvents(ctx, int32(batchSize))
	if err != nil {
		return nil, err
	}
	events := make([]Event, 0, len(rows))
	for _, row := range rows {
		events = append(events, eventFromQuery(row))
	}
	return events, nil
}

func (repo *Repository) MarkPublished(ctx context.Context, id uuid.UUID) error {
	return repo.queries.MarkOutboxEventPublished(ctx, pgUUID(id))
}

func eventFromQuery(row queries.OutboxEvent) Event {
	return Event{
		ID:           uuidFromPG(row.ID),
		Topic:        row.Topic,
		PartitionKey: row.PartitionKey,
		Payload:      append([]byte(nil), row.Payload...),
		Traceparent:  row.Traceparent,
		Tracestate:   row.Tracestate,
		ClaimedAt:    timeFromPG(row.ClaimedAt),
		PublishedAt:  timeFromPG(row.PublishedAt),
		CreatedAt:    row.CreatedAt.Time,
	}
}

func timeFromPG(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	return &value.Time
}

func pgUUID(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}

func uuidFromPG(id pgtype.UUID) uuid.UUID {
	if !id.Valid {
		return uuid.Nil
	}
	return uuid.UUID(id.Bytes)
}
