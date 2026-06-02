package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"

	ledgerv1 "enjoythings/services/gen/ledger/v1"
	"enjoythings/services/internal/config"
	"enjoythings/services/internal/ledgerconsumer"
	"enjoythings/services/internal/ledgergrpc"
	"enjoythings/services/internal/repo"
	"enjoythings/services/internal/wallet"

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
	ledgerv1.RegisterLedgerServiceServer(grpcServer, ledgergrpc.NewServer(wallet.NewService(db)))

	errCh := make(chan error, 1)
	go func() {
		slog.Info("ledger grpc listening", "addr", cfg.GRPCAddr, "env", cfg.AppEnv)
		errCh <- grpcServer.Serve(listener)
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
		return nil
	case err := <-errCh:
		if errors.Is(err, grpc.ErrServerStopped) {
			return nil
		}
		return err
	}
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
