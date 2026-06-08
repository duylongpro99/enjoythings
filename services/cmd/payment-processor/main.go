package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"enjoythings/services/internal/config"
	healthhandler "enjoythings/services/internal/handler"
	"enjoythings/services/internal/outbox"
	"enjoythings/services/internal/paymentprocessor"
	"enjoythings/services/internal/repo"
	"enjoythings/services/internal/telemetry"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

func main() {
	if err := run(); err != nil {
		slog.Error("payment processor stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.LoadPaymentProcessor()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	shutdownTracing, err := telemetry.Init(ctx, "payment-processor", cfg.AppEnv)
	if err != nil {
		slog.Warn("telemetry init failed", "error", err)
	}
	defer func() { _ = shutdownTracing(context.Background()) }()

	db, err := repo.Connect(ctx, cfg.DatabaseURL, cfg.DBMaxConns)
	if err != nil {
		return err
	}
	defer db.Close()
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

	rail := paymentprocessor.NewHTTPRail(cfg.PaymentRailURL, &http.Client{Timeout: cfg.PaymentRailTimeout, Transport: otelhttp.NewTransport(http.DefaultTransport)})
	processor := paymentprocessor.NewProcessor(
		db.PaymentAttemptStore(),
		rail,
		outboxAdapter{repo: db.OutboxRepository()},
		paymentprocessor.ProcessorConfig{},
	)
	consumer, err := paymentprocessor.NewKafkaConsumer(splitCSV(cfg.KafkaBrokers), cfg.PaymentProcessorGroupID, processor, slog.Default())
	if err != nil {
		return err
	}
	defer consumer.Close()

	httpServer := healthServer(cfg.HTTPAddr, db)
	errCh := make(chan error, 1)
	go func() {
		slog.Info("payment processor health listening", "addr", cfg.HTTPAddr, "env", cfg.AppEnv)
		errCh <- httpServer.ListenAndServe()
	}()
	go func() {
		slog.Info("payment processor kafka consumer started", "group_id", cfg.PaymentProcessorGroupID, "rail_url", cfg.PaymentRailURL)
		consumer.Run(ctx)
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdownCtx)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
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
		Handler:           otelhttp.NewHandler(mux, "payment-processor.health.http"),
		ReadHeaderTimeout: 5 * time.Second,
	}
}
