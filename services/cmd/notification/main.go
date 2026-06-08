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
	"enjoythings/services/internal/notification"
	"enjoythings/services/internal/telemetry"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

func main() {
	if err := run(); err != nil {
		slog.Error("notification stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.LoadNotification()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	shutdownTracing, err := telemetry.Init(ctx, "notification", cfg.AppEnv)
	if err != nil {
		slog.Warn("telemetry init failed", "error", err)
	}
	defer func() { _ = shutdownTracing(context.Background()) }()

	email := notification.NewStubEmailAdapter(slog.Default())
	sms := notification.NewStubSMSAdapter(slog.Default())
	dispatcher := notification.NewDispatcher(email, sms, slog.Default())
	consumer, err := notification.NewKafkaConsumer(splitCSV(cfg.KafkaBrokers), cfg.NotificationConsumerGroupID, dispatcher, slog.Default())
	if err != nil {
		return err
	}
	defer consumer.Close()

	httpServer := healthServer(cfg.HTTPAddr)
	errCh := make(chan error, 1)
	go func() {
		slog.Info("notification health listening", "addr", cfg.HTTPAddr, "env", cfg.AppEnv)
		errCh <- httpServer.ListenAndServe()
	}()
	go func() {
		slog.Info("notification kafka consumer started", "group_id", cfg.NotificationConsumerGroupID)
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

func healthServer(addr string) *http.Server {
	mux := http.NewServeMux()
	mux.Handle("/healthz", healthhandler.Health())
	mux.Handle("/readyz", healthhandler.Health())
	return &http.Server{
		Addr:              addr,
		Handler:           otelhttp.NewHandler(mux, "notification.health.http"),
		ReadHeaderTimeout: 5 * time.Second,
	}
}
