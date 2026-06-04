package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"enjoythings/services/internal/paymentprocessor"
)

func main() {
	if err := run(); err != nil {
		slog.Error("stub payment rail stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	addr := valueOrDefault("HTTP_ADDR", ":18090")
	timeoutSleep := 3 * time.Second
	if raw := os.Getenv("STUB_RAIL_TIMEOUT_SLEEP"); raw != "" {
		value, err := time.ParseDuration(raw)
		if err != nil || value <= 0 {
			return errors.New("STUB_RAIL_TIMEOUT_SLEEP must be a positive duration")
		}
		timeoutSleep = value
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	server := &http.Server{
		Addr:              addr,
		Handler:           paymentprocessor.NewStubRailHandler(timeoutSleep),
		ReadHeaderTimeout: 5 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		slog.Info("stub payment rail listening", "addr", addr)
		errCh <- server.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdownCtx)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func valueOrDefault(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}
