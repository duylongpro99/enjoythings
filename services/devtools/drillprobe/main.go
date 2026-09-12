// Command drillprobe is the black-box assertion behind drill scenario probes.
// It creates funded transfers through the gateway, exactly as a user would, and
// asserts what state they reach. Exit 0 means the assertion held.
//
//	drillprobe -want PAYMENT_PROCESSING -after 15s          # symptom present
//	drillprobe -want COMPLETED -within 30s -count 10        # symptom gone
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"enjoythings/services/devtools/smoke"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	defaultGatewayURL  = "http://localhost:8080"
	defaultDatabaseURL = "postgres://enjoythings:enjoythings_dev_password@localhost:5432/enjoythings?sslmode=disable"
	defaultJWTSecret   = "local-dev-jwt-secret-change-me"
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "drillprobe: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("drillprobe: ok")
}

func run(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("drillprobe", flag.ContinueOnError)
	var gatewayURL, databaseURL, jwtSecret, want string
	var count int
	var amount int64
	var after, within time.Duration
	fs.StringVar(&gatewayURL, "gateway-url", smoke.GetenvDefault("GATEWAY_URL", defaultGatewayURL), "gateway base URL")
	fs.StringVar(&databaseURL, "database-url", smoke.GetenvDefault("DATABASE_URL", defaultDatabaseURL), "Postgres URL used to fund the source wallet")
	fs.StringVar(&jwtSecret, "jwt-secret", smoke.GetenvDefault("JWT_SECRET", defaultJWTSecret), "local JWT secret")
	fs.StringVar(&want, "want", "COMPLETED", "saga state the transfers must be in")
	fs.IntVar(&count, "count", 1, "number of transfers to submit")
	fs.Int64Var(&amount, "amount", 1250, "amount per transfer in cents")
	fs.DurationVar(&after, "after", 0, "wait this long, then assert every transfer is in -want (hold mode)")
	fs.DurationVar(&within, "within", 0, "assert every transfer reaches -want within this window (settle mode)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if (after == 0) == (within == 0) {
		return errors.New("exactly one of -after or -within is required")
	}
	if count <= 0 {
		return errors.New("-count must be positive")
	}

	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute+after+within)
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
		return fmt.Errorf("connect database: %w", err)
	}
	defer db.Close()
	if err := client.SubmitVerification(ctx); err != nil {
		return err
	}
	from, to, err := client.CreateWalletPair(ctx)
	if err != nil {
		return err
	}
	if err := smoke.SetBalance(ctx, db, from, amount*int64(count)*2); err != nil {
		return err
	}

	ids := make([]string, 0, count)
	for i := 0; i < count; i++ {
		payment, err := client.StartPayment(ctx, from, to, amount, http.StatusAccepted)
		if err != nil {
			return fmt.Errorf("transfer %d: %w", i+1, err)
		}
		ids = append(ids, payment.PaymentID)
	}

	if after > 0 {
		time.Sleep(after)
		for _, id := range ids {
			payment, err := client.GetPayment(ctx, id)
			if err != nil {
				return err
			}
			if payment.Status != want {
				return fmt.Errorf("payment %s is %s after %s, want %s", id, payment.Status, after, want)
			}
		}
		return nil
	}
	settleCtx, cancelSettle := context.WithTimeout(ctx, within)
	defer cancelSettle()
	for _, id := range ids {
		if err := client.WaitPaymentState(settleCtx, id, want); err != nil {
			return err
		}
	}
	return nil
}
