package repo

import (
	"context"

	"enjoythings/services/internal/repo/queries"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Database struct {
	pool    *pgxpool.Pool
	queries *queries.Queries
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

	db := &Database{pool: pool, queries: queries.New(pool)}
	if err := db.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}

	return db, nil
}

func (db *Database) Ping(ctx context.Context) error {
	return db.pool.Ping(ctx)
}

func (db *Database) Close() {
	db.pool.Close()
}
