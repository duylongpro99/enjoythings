package saga

import (
	"context"
	"log/slog"
	"time"

	"enjoythings/services/internal/telemetry"
)

const (
	defaultFraudAuditSweepInterval = time.Hour
	defaultFraudAuditSweepBatch    = 1000
)

type fraudAuditRetentionStore interface {
	DeleteFraudAuditBefore(context.Context, time.Time, int) (int64, error)
}

type FraudAuditSweeperConfig struct {
	// Retention is how long an audit row is kept after it was written. Zero
	// disables the sweeper entirely: rows are kept forever.
	Retention     time.Duration
	SweepInterval time.Duration
	BatchSize     int
}

// FraudAuditSweeper deletes saga fraud audit rows past their retention window.
// It runs like the outbox publisher — one background loop per orchestrator —
// and drains in bounded batches so a long backlog never turns into one large
// delete holding locks against the live audit writes.
type FraudAuditSweeper struct {
	store  fraudAuditRetentionStore
	cfg    FraudAuditSweeperConfig
	clock  Clock
	logger *slog.Logger
}

func NewFraudAuditSweeper(store fraudAuditRetentionStore, cfg FraudAuditSweeperConfig, clock Clock, logger *slog.Logger) *FraudAuditSweeper {
	if cfg.SweepInterval <= 0 {
		cfg.SweepInterval = defaultFraudAuditSweepInterval
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = defaultFraudAuditSweepBatch
	}
	if clock == nil {
		clock = systemClock{}
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &FraudAuditSweeper{store: store, cfg: cfg, clock: clock, logger: logger}
}

func (sweeper *FraudAuditSweeper) Run(ctx context.Context) {
	if sweeper.cfg.Retention <= 0 {
		return
	}
	ticker := time.NewTicker(sweeper.cfg.SweepInterval)
	defer ticker.Stop()

	for {
		if _, err := sweeper.Sweep(ctx); err != nil && ctx.Err() == nil {
			sweeper.logger.Error("fraud audit retention sweep failed", "error", err)
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Sweep deletes every eligible row older than the retention window, one batch
// at a time, and returns how many rows were removed. A zero retention deletes
// nothing.
func (sweeper *FraudAuditSweeper) Sweep(ctx context.Context) (int64, error) {
	if sweeper.cfg.Retention <= 0 {
		return 0, nil
	}
	cutoff := sweeper.clock.Now().Add(-sweeper.cfg.Retention)
	var deleted int64
	for {
		count, err := sweeper.store.DeleteFraudAuditBefore(ctx, cutoff, sweeper.cfg.BatchSize)
		if err != nil {
			return deleted, err
		}
		deleted += count
		telemetry.ServiceMetrics("saga-orchestrator").RecordFraudAuditDeleted(count)
		if count < int64(sweeper.cfg.BatchSize) {
			break
		}
	}
	if deleted > 0 {
		sweeper.logger.Info("fraud audit retention sweep deleted rows", "deleted", deleted, "cutoff", cutoff)
	}
	return deleted, nil
}
