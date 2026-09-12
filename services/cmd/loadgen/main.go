// Command loadgen drives constant-rate transfers through the gateway for a
// drill and exports latency, error, and settlement metrics on /metrics.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"enjoythings/services/devtools/smoke"
	"enjoythings/services/internal/loadgen"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	defaultGatewayURL  = "http://gateway:8080"
	defaultDatabaseURL = "postgres://enjoythings:enjoythings_dev_password@postgres:5432/enjoythings?sslmode=disable"
	defaultJWTSecret   = "local-dev-jwt-secret-change-me"
	// Each account is funded once at start-up. At the default rate and amount
	// this lasts well over a day, which outlives any drill.
	seedBalanceCents = 1_000_000_000
)

func main() {
	if err := run(); err != nil {
		slog.Error("loadgen stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	config, users, addr, err := loadConfig()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	metrics := loadgen.NewMetrics()
	mux := http.NewServeMux()
	mux.Handle("/metrics", metrics.Handler())
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	server := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("metrics server stopped", "error", err)
		}
	}()
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()

	accounts, err := seedAccounts(ctx, users)
	if err != nil {
		return err
	}
	runner, err := loadgen.NewRunner(config, accounts, metrics)
	if err != nil {
		return err
	}
	slog.Info("loadgen running", "rate", config.Rate, "accounts", len(accounts), "amount_cents", config.AmountCents)
	runner.Run(ctx)
	slog.Info("loadgen drained")
	return nil
}

func loadConfig() (loadgen.Config, int, string, error) {
	rate, err := strconv.ParseFloat(smoke.GetenvDefault("LOADGEN_RATE", "5"), 64)
	if err != nil {
		return loadgen.Config{}, 0, "", fmt.Errorf("LOADGEN_RATE: %w", err)
	}
	users, err := strconv.Atoi(smoke.GetenvDefault("LOADGEN_USERS", "20"))
	if err != nil || users <= 0 {
		return loadgen.Config{}, 0, "", errors.New("LOADGEN_USERS must be a positive integer")
	}
	// Matches the gateway default of one token per second per user
	// (RATE_LIMIT_REFILL_EVERY=1s); change both together.
	budget, err := strconv.ParseFloat(smoke.GetenvDefault("LOADGEN_USER_BUDGET_RPS", "1"), 64)
	if err != nil {
		return loadgen.Config{}, 0, "", fmt.Errorf("LOADGEN_USER_BUDGET_RPS: %w", err)
	}
	amount, err := strconv.ParseInt(smoke.GetenvDefault("LOADGEN_AMOUNT_CENTS", "1250"), 10, 64)
	if err != nil {
		return loadgen.Config{}, 0, "", fmt.Errorf("LOADGEN_AMOUNT_CENTS: %w", err)
	}
	settle, err := time.ParseDuration(smoke.GetenvDefault("LOADGEN_SETTLE_TIMEOUT", "60s"))
	if err != nil {
		return loadgen.Config{}, 0, "", fmt.Errorf("LOADGEN_SETTLE_TIMEOUT: %w", err)
	}
	config := loadgen.Config{Rate: rate, AmountCents: amount, SettleTimeout: settle, PollInterval: 250 * time.Millisecond, UserBudget: budget}
	if err := config.Validate(); err != nil {
		return loadgen.Config{}, 0, "", err
	}
	return config, users, smoke.GetenvDefault("HTTP_ADDR", ":8080"), nil
}

// seedAccounts creates one verified user with a funded wallet pair per account.
// Funding writes the platform database directly, as the smoke suites do,
// because no product endpoint deposits money.
func seedAccounts(ctx context.Context, users int) ([]loadgen.Account, error) {
	gatewayURL := smoke.GetenvDefault("GATEWAY_URL", defaultGatewayURL)
	jwtSecret := smoke.GetenvDefault("JWT_SECRET", defaultJWTSecret)
	db, err := pgxpool.New(ctx, smoke.GetenvDefault("DATABASE_URL", defaultDatabaseURL))
	if err != nil {
		return nil, fmt.Errorf("connect database: %w", err)
	}
	defer db.Close()

	readyCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	accounts := make([]loadgen.Account, 0, users)
	for i := 0; i < users; i++ {
		client, err := smoke.NewClient(gatewayURL, jwtSecret)
		if err != nil {
			return nil, err
		}
		if i == 0 {
			if err := client.WaitReady(readyCtx); err != nil {
				return nil, err
			}
		}
		if err := client.SubmitVerification(ctx); err != nil {
			return nil, fmt.Errorf("verify user %d: %w", i, err)
		}
		from, to, err := client.CreateWalletPair(ctx)
		if err != nil {
			return nil, fmt.Errorf("wallets for user %d: %w", i, err)
		}
		if err := smoke.SetBalance(ctx, db, from, seedBalanceCents); err != nil {
			return nil, err
		}
		accounts = append(accounts, loadgen.Account{Gateway: client, From: from, To: to})
	}
	return accounts, nil
}
