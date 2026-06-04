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

	sagav1 "enjoythings/services/gen/saga/v1"
	"enjoythings/services/internal/config"
	healthhandler "enjoythings/services/internal/handler"
	"enjoythings/services/internal/outbox"
	"enjoythings/services/internal/repo"
	"enjoythings/services/internal/saga"
	"enjoythings/services/internal/sagaconsumer"
	"enjoythings/services/internal/sagagrpc"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func main() {
	if err := run(); err != nil {
		slog.Error("saga orchestrator stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.LoadSaga()
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

	walletConn, err := grpc.NewClient(cfg.WalletGRPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer walletConn.Close()
	ledgerConn, err := grpc.NewClient(cfg.LedgerGRPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer ledgerConn.Close()
	verificationConn, err := grpc.NewClient(cfg.VerificationGRPCAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return err
	}
	defer verificationConn.Close()

	producer, err := outbox.NewKafkaProducer(splitCSV(cfg.KafkaBrokers))
	if err != nil {
		return err
	}
	defer producer.Close()

	orchestrator := saga.NewOrchestrator(
		db.SagaStore(),
		saga.NewVerificationGRPCClient(verificationConn),
		saga.NewWalletGRPCClient(walletConn),
		saga.NewLedgerGRPCClient(ledgerConn),
		nil,
	)
	orchestrator.SetOutbox(outboxAdapter{repo: db.OutboxRepository()})
	if err := orchestrator.ResumeNonTerminal(ctx); err != nil {
		return err
	}

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

	var consumer *sagaconsumer.KafkaConsumer
	if cfg.SagaConsumerEnabled {
		consumer, err = sagaconsumer.NewKafkaConsumer(splitCSV(cfg.KafkaBrokers), cfg.SagaConsumerGroupID, orchestrator, slog.Default())
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
	sagav1.RegisterSagaServiceServer(grpcServer, sagagrpc.NewServer(orchestrator))
	httpServer := healthServer(cfg.HTTPAddr, db)

	errCh := make(chan error, 1)
	go func() {
		slog.Info("saga orchestrator grpc listening", "addr", cfg.GRPCAddr, "env", cfg.AppEnv)
		errCh <- grpcServer.Serve(listener)
	}()
	go func() {
		slog.Info("saga orchestrator health listening", "addr", cfg.HTTPAddr, "env", cfg.AppEnv)
		errCh <- httpServer.ListenAndServe()
	}()
	if consumer != nil {
		go func() {
			slog.Info("saga orchestrator kafka consumer started", "group_id", cfg.SagaConsumerGroupID)
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

type outboxAdapter struct {
	repo *outbox.Repository
}

func (adapter outboxAdapter) Enqueue(ctx context.Context, topic, partitionKey string, payload []byte) error {
	_, err := adapter.repo.Enqueue(ctx, topic, partitionKey, payload)
	return err
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
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
}
