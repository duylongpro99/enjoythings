package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"enjoythings/services/devtools/smoke"
	"enjoythings/services/internal/paymentprocessor"
	"enjoythings/services/internal/saga"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	defaultGatewayURL  = "http://localhost:8080"
	defaultDatabaseURL = "postgres://enjoythings:enjoythings_dev_password@localhost:5432/enjoythings?sslmode=disable"
	defaultJWTSecret   = "local-dev-jwt-secret-change-me"
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "phase3 smoke: %v\n", err)
		os.Exit(1)
	}
	_, _ = fmt.Fprintln(os.Stdout, "phase3 smoke: ok")
}

func run(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("phase3_smoke", flag.ContinueOnError)
	var gatewayURL string
	var databaseURL string
	var jwtSecret string
	var timeout time.Duration
	fs.StringVar(&gatewayURL, "gateway-url", smoke.GetenvDefault("GATEWAY_URL", defaultGatewayURL), "gateway base URL")
	fs.StringVar(&databaseURL, "database-url", smoke.GetenvDefault("DATABASE_URL", defaultDatabaseURL), "Postgres URL for local smoke assertions")
	fs.StringVar(&jwtSecret, "jwt-secret", smoke.GetenvDefault("JWT_SECRET", defaultJWTSecret), "local JWT secret")
	fs.DurationVar(&timeout, "timeout", 90*time.Second, "overall smoke timeout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	client, err := smoke.NewClient(gatewayURL, jwtSecret)
	if err != nil {
		return err
	}
	if err := client.WaitReady(ctx); err != nil {
		return err
	}
	db, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return fmt.Errorf("connect smoke database: %w", err)
	}
	defer db.Close()

	from, to, err := client.CreateWalletPair(ctx)
	if err != nil {
		return err
	}
	if err := smoke.SetBalance(ctx, db, from, 5000); err != nil {
		return err
	}
	if _, err := client.StartPayment(ctx, from, to, 1250, http.StatusUnprocessableEntity); err != nil {
		return fmt.Errorf("unverified gateway boundary: %w", err)
	}
	if err := client.SubmitVerification(ctx); err != nil {
		return fmt.Errorf("verification boundary: %w", err)
	}

	if err := runScenario(ctx, client, db, 1250, 5000, saga.StateCompleted, "CONFIRMED", saga.TopicTxCompleted, 1); err != nil {
		return fmt.Errorf("happy path boundary: %w", err)
	}
	if err := runScenario(ctx, client, db, paymentprocessor.StubTerminalFailureAmountCents, 50000, saga.StateFailed, "CANCELED", saga.TopicTxFailed, 1); err != nil {
		return fmt.Errorf("terminal failure boundary: %w", err)
	}
	if err := runScenario(ctx, client, db, paymentprocessor.StubRetryOnceAmountCents, 60000, saga.StateCompleted, "CONFIRMED", saga.TopicTxCompleted, 2); err != nil {
		return fmt.Errorf("payment retry boundary: %w", err)
	}
	return nil
}

func runScenario(ctx context.Context, client *smoke.Client, db *pgxpool.Pool, amount, initialBalance int64, sagaState, ledgerStatus, topic string, attempts int) error {
	from, to, err := client.CreateWalletPair(ctx)
	if err != nil {
		return err
	}
	if err := smoke.SetBalance(ctx, db, from, initialBalance); err != nil {
		return err
	}
	payment, err := client.StartPayment(ctx, from, to, amount, http.StatusAccepted)
	if err != nil {
		return err
	}
	if err := client.WaitPaymentState(ctx, payment.PaymentID, sagaState); err != nil {
		return err
	}
	if err := waitDatabaseState(ctx, db, payment.PaymentID, ledgerStatus, topic, attempts); err != nil {
		return err
	}
	var balance int64
	if err := db.QueryRow(ctx, `SELECT balance FROM wallets WHERE id = $1`, from).Scan(&balance); err != nil {
		return fmt.Errorf("read source balance: %w", err)
	}
	wantBalance := initialBalance - amount
	if sagaState == saga.StateFailed {
		wantBalance = initialBalance
	}
	if balance != wantBalance {
		return fmt.Errorf("wallet balance = %d, want %d", balance, wantBalance)
	}
	return nil
}

func waitDatabaseState(ctx context.Context, db *pgxpool.Pool, paymentID, ledgerStatus, topic string, attempts int) error {
	return smoke.Poll(ctx, func() (bool, error) {
		var gotLedgerStatus string
		if err := db.QueryRow(ctx, `SELECT status FROM ledger_transfer_reservations WHERE payment_id = $1`, paymentID).Scan(&gotLedgerStatus); err != nil {
			return false, nil
		}
		var eventCount int
		if err := db.QueryRow(ctx, `SELECT count(*) FROM outbox_events WHERE topic = $1 AND payload->>'payment_id' = $2`, topic, paymentID).Scan(&eventCount); err != nil {
			return false, err
		}
		var attemptCount int
		if err := db.QueryRow(ctx, `SELECT attempt_count FROM payment_attempts WHERE payment_id = $1`, paymentID).Scan(&attemptCount); err != nil {
			return false, nil
		}
		return gotLedgerStatus == ledgerStatus && eventCount > 0 && attemptCount == attempts, nil
	}, fmt.Sprintf("database payment=%s ledger=%s topic=%s attempt_count=%d", paymentID, ledgerStatus, topic, attempts))
}
