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

	walletv1 "enjoythings/services/gen/wallet/v1"
	"enjoythings/services/internal/config"
	healthhandler "enjoythings/services/internal/handler"
	"enjoythings/services/internal/outbox"
	"enjoythings/services/internal/repo"
	"enjoythings/services/internal/telemetry"
	"enjoythings/services/internal/wallet"
	"enjoythings/services/internal/walletgrpc"

	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"google.golang.org/grpc"
)

func main() {
	if err := run(); err != nil {
		slog.Error("wallet stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.LoadWallet()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	shutdownTracing, err := telemetry.Init(ctx, "wallet", cfg.AppEnv)
	if err != nil {
		slog.Warn("telemetry init failed", "error", err)
	}
	defer func() { _ = shutdownTracing(context.Background()) }()

	db, err := repo.Connect(ctx, cfg.DatabaseURL, cfg.DBMaxConns)
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetOutboxTopic(cfg.WalletOutboxTopic)
	if err := db.RunMigrations(ctx); err != nil {
		return err
	}

	producer, err := outbox.NewKafkaProducer(splitCSV(cfg.KafkaBrokers))
	if err != nil {
		return err
	}
	defer producer.Close()

	publisher := outbox.NewPublisher(
		db.OutboxRepository(),
		producer,
		outbox.PublisherConfig{
			PollInterval: cfg.WalletOutboxPollInterval,
			BatchSize:    cfg.WalletOutboxBatchSize,
		},
		slog.Default(),
	)
	go publisher.Run(ctx)

	listener, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		return err
	}
	defer func() { _ = listener.Close() }()

	grpcServer := grpc.NewServer(grpc.StatsHandler(otelgrpc.NewServerHandler()), grpc.UnaryInterceptor(telemetry.ServiceMetrics("wallet").UnaryServerInterceptor()))
	walletv1.RegisterWalletServiceServer(grpcServer, walletgrpc.NewServer(wallet.NewService(db)))
	httpServer := healthServer(cfg.HTTPAddr, db)

	errCh := make(chan error, 1)
	go func() {
		slog.Info("wallet grpc listening", "addr", cfg.GRPCAddr, "env", cfg.AppEnv)
		errCh <- grpcServer.Serve(listener)
	}()
	go func() {
		slog.Info("wallet health listening", "addr", cfg.HTTPAddr, "env", cfg.AppEnv)
		errCh <- httpServer.ListenAndServe()
	}()

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

func splitCSV(value string) []string {
	parts := strings.Split(value, ",")
	items := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			items = append(items, part)
		}
	}
	return items
}

func healthServer(addr string, db *repo.Database) *http.Server {
	mux := http.NewServeMux()
	mux.Handle("/healthz", healthhandler.Health())
	mux.Handle("/readyz", healthhandler.Ready(db))
	return &http.Server{
		Addr:              addr,
		Handler:           otelhttp.NewHandler(telemetry.InstrumentHTTP("wallet", mux), "wallet.health.http"),
		ReadHeaderTimeout: 5 * time.Second,
	}
}
