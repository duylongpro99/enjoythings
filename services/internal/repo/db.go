package repo

import (
	"context"

	"enjoythings/services/internal/event"
	"enjoythings/services/internal/outbox"
	"enjoythings/services/internal/paymentprocessor"
	"enjoythings/services/internal/repo/queries"
	"enjoythings/services/internal/saga"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Database struct {
	pool        *pgxpool.Pool
	queries     *queries.Queries
	outboxTopic string
}

func NewPoolConfig(databaseURL string, maxConns int32) (*pgxpool.Config, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = maxConns
	return cfg, nil
}

func Connect(ctx context.Context, databaseURL string, maxConns int32) (*Database, error) {
	cfg, err := NewPoolConfig(databaseURL, maxConns)
	if err != nil {
		return nil, err
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}

	db := &Database{pool: pool, queries: queries.New(pool), outboxTopic: event.TransactionInitiatedTopic}
	if err := db.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}

	return db, nil
}

func (db *Database) SetOutboxTopic(topic string) {
	if topic != "" {
		db.outboxTopic = topic
	}
}

func (db *Database) OutboxRepository() *outbox.Repository {
	return outbox.NewRepository(db.pool)
}

func (db *Database) SagaStore() *saga.PostgresStore {
	return saga.NewPostgresStore(db.pool)
}

func (db *Database) PaymentAttemptStore() *paymentprocessor.PostgresStore {
	return paymentprocessor.NewPostgresStore(db.pool)
}

func (db *Database) Ping(ctx context.Context) error {
	return db.pool.Ping(ctx)
}

func (db *Database) Close() {
	db.pool.Close()
}
