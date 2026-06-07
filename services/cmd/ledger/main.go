package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	ledgerv1 "enjoythings/services/gen/ledger/v1"
	"enjoythings/services/internal/config"
	"enjoythings/services/internal/domain"
	healthhandler "enjoythings/services/internal/handler"
	"enjoythings/services/internal/ledgerconsumer"
	"enjoythings/services/internal/ledgergrpc"
	"enjoythings/services/internal/repo"
	"enjoythings/services/internal/wallet"

	"github.com/google/uuid"
	"google.golang.org/grpc"
)

func main() {
	if err := run(); err != nil {
		slog.Error("ledger stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.LoadLedger()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := repo.Connect(ctx, cfg.DatabaseURL, cfg.DBMaxConns)
	if err != nil {
		return err
	}
	defer db.Close()
	if err := db.RunMigrations(ctx); err != nil {
		return err
	}

	var consumer *ledgerconsumer.KafkaConsumer
	if cfg.LedgerConsumerEnabled {
		consumer, err = ledgerconsumer.NewKafkaConsumer(
			brokersFromConfig(cfg.KafkaBrokers),
			cfg.LedgerConsumerTopic,
			cfg.LedgerConsumerGroupID,
			db,
			slog.Default(),
		)
		if err != nil {
			return err
		}
		defer consumer.Close()
	}

	listener, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		return err
	}
	defer listener.Close()

	grpcServer := grpc.NewServer()
	ledgerv1.RegisterLedgerServiceServer(grpcServer, ledgergrpc.NewServer(newLedgerApp(db)))
	httpServer := healthServer(cfg.HTTPAddr, db)

	errCh := make(chan error, 1)
	go func() {
		slog.Info("ledger grpc listening", "addr", cfg.GRPCAddr, "env", cfg.AppEnv)
		errCh <- grpcServer.Serve(listener)
	}()
	go func() {
		slog.Info("ledger health listening", "addr", cfg.HTTPAddr, "env", cfg.AppEnv)
		errCh <- httpServer.ListenAndServe()
	}()
	if consumer != nil {
		go func() {
			slog.Info("ledger kafka consumer started", "topic", cfg.LedgerConsumerTopic, "group_id", cfg.LedgerConsumerGroupID)
			consumer.Run(ctx)
		}()
	}

	select {
	case <-ctx.Done():
		grpcServer.GracefulStop()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdownCtx)
	case err := <-errCh:
		if errors.Is(err, grpc.ErrServerStopped) || errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

type ledgerApp struct {
	reader *wallet.Service
	store  *repo.Database
}

func newLedgerApp(store *repo.Database) *ledgerApp {
	return &ledgerApp{
		reader: wallet.NewService(store),
		store:  store,
	}
}

func (app *ledgerApp) ListLedger(ctx context.Context, userID, walletID uuid.UUID, cursor repo.LedgerCursor, limit int) ([]domain.LedgerEntry, repo.LedgerCursor, error) {
	return app.reader.ListLedger(ctx, userID, walletID, cursor, limit)
}

func (app *ledgerApp) GetFraudTransactionHistory(ctx context.Context, walletID uuid.UUID, limit int, traceID string) ([]domain.FraudTransactionSummary, error) {
	return app.store.GetFraudTransactionHistory(ctx, walletID, limit, traceID)
}

func (app *ledgerApp) GetFraudVelocityMetrics(ctx context.Context, walletID uuid.UUID, asOf time.Time, traceID string) (domain.FraudVelocityMetrics, error) {
	return app.store.GetFraudVelocityMetrics(ctx, walletID, asOf, traceID)
}

func (app *ledgerApp) ReserveTransfer(ctx context.Context, cmd domain.LedgerReserveCommand) (domain.LedgerReservation, error) {
	return app.store.ReserveTransfer(ctx, cmd)
}

func (app *ledgerApp) ConfirmTransfer(ctx context.Context, cmd domain.LedgerConfirmCommand) (domain.LedgerConfirmation, error) {
	return app.store.ConfirmTransfer(ctx, cmd)
}

func (app *ledgerApp) CancelReservation(ctx context.Context, cmd domain.LedgerCancelCommand) (domain.LedgerReservation, error) {
	return app.store.CancelReservation(ctx, cmd)
}

func brokersFromConfig(raw string) []string {
	parts := strings.Split(raw, ",")
	brokers := make([]string, 0, len(parts))
	for _, part := range parts {
		broker := strings.TrimSpace(part)
		if broker != "" {
			brokers = append(brokers, broker)
		}
	}
	return brokers
}

func healthServer(addr string, db *repo.Database) *http.Server {
	mux := http.NewServeMux()
	mux.Handle("/healthz", healthhandler.Health())
	mux.Handle("/readyz", healthhandler.Ready(db))
	return &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
}
