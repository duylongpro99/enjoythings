// Command phase4_smoke validates the fraud and observability behavior of a
// running Compose stack: one payment produces a fraud audit session, healthy
// Prometheus targets, a saga-to-worker trace, and provisioned dashboards.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"enjoythings/services/devtools/smoke"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	defaultGatewayURL       = "http://localhost:8080"
	defaultDatabaseURL      = "postgres://enjoythings:enjoythings_dev_password@localhost:5432/enjoythings?sslmode=disable"
	defaultFraudDatabaseURL = "postgres://fraud_worker:change-me@localhost:5433/fraud_audit?sslmode=disable"
	defaultPrometheusURL    = "http://localhost:9095"
	defaultJaegerURL        = "http://localhost:16686"
	defaultGrafanaURL       = "http://localhost:3001"
	defaultJWTSecret        = "local-dev-jwt-secret-change-me"

	// boundaryTimeout caps every observability wait, as Phase 4.9 requires.
	boundaryTimeout = 30 * time.Second

	sagaService  = "saga-orchestrator"
	fraudService = "fraud-worker"
)

type config struct {
	gatewayURL       string
	databaseURL      string
	fraudDatabaseURL string
	prometheusURL    string
	jaegerURL        string
	grafanaURL       string
	grafanaUser      string
	grafanaPassword  string
	jwtSecret        string
	timeout          time.Duration
}

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "phase4 smoke: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stdout, "phase4 smoke: ok")
}

func run(ctx context.Context, args []string) error {
	cfg, err := parseConfig(args)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, cfg.timeout)
	defer cancel()

	paymentID, err := scoreOnePayment(ctx, cfg)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "phase4 smoke: scored payment %s\n", paymentID)

	if err := checkAuditSession(ctx, cfg, paymentID); err != nil {
		return fmt.Errorf("fraud audit boundary: %w", err)
	}
	if err := checkPrometheus(ctx, cfg); err != nil {
		return fmt.Errorf("prometheus boundary: %w", err)
	}
	if err := checkTrace(ctx, cfg, paymentID); err != nil {
		return fmt.Errorf("jaeger boundary: %w", err)
	}
	if err := checkDashboards(ctx, cfg); err != nil {
		return fmt.Errorf("grafana boundary: %w", err)
	}
	return nil
}

func parseConfig(args []string) (config, error) {
	fs := flag.NewFlagSet("phase4_smoke", flag.ContinueOnError)
	var cfg config
	fs.StringVar(&cfg.gatewayURL, "gateway-url", smoke.GetenvDefault("GATEWAY_URL", defaultGatewayURL), "gateway base URL")
	fs.StringVar(&cfg.databaseURL, "database-url", smoke.GetenvDefault("DATABASE_URL", defaultDatabaseURL), "platform Postgres URL")
	fs.StringVar(&cfg.fraudDatabaseURL, "fraud-database-url", smoke.GetenvDefault("FRAUD_SMOKE_DATABASE_URL", defaultFraudDatabaseURL), "fraud audit database URL reachable from the host")
	fs.StringVar(&cfg.prometheusURL, "prometheus-url", smoke.GetenvDefault("PROMETHEUS_URL", defaultPrometheusURL), "Prometheus base URL")
	fs.StringVar(&cfg.jaegerURL, "jaeger-url", smoke.GetenvDefault("JAEGER_URL", defaultJaegerURL), "Jaeger query base URL")
	fs.StringVar(&cfg.grafanaURL, "grafana-url", smoke.GetenvDefault("GRAFANA_URL", defaultGrafanaURL), "Grafana base URL")
	fs.StringVar(&cfg.grafanaUser, "grafana-user", smoke.GetenvDefault("GRAFANA_ADMIN_USER", "admin"), "Grafana admin user")
	fs.StringVar(&cfg.grafanaPassword, "grafana-password", os.Getenv("GRAFANA_ADMIN_PASSWORD"), "Grafana admin password")
	fs.StringVar(&cfg.jwtSecret, "jwt-secret", smoke.GetenvDefault("JWT_SECRET", defaultJWTSecret), "local JWT secret")
	fs.DurationVar(&cfg.timeout, "timeout", 3*time.Minute, "overall smoke timeout")
	if err := fs.Parse(args); err != nil {
		return config{}, err
	}
	if cfg.grafanaPassword == "" {
		return config{}, fmt.Errorf("GRAFANA_ADMIN_PASSWORD is required to query dashboards")
	}
	return cfg, nil
}

// scoreOnePayment drives one verified payment so the saga publishes
// fraud.score.requested and the worker produces one audited session.
func scoreOnePayment(ctx context.Context, cfg config) (string, error) {
	client, err := smoke.NewClient(cfg.gatewayURL, cfg.jwtSecret)
	if err != nil {
		return "", err
	}
	if err := client.WaitReady(ctx); err != nil {
		return "", fmt.Errorf("gateway boundary: %w", err)
	}
	db, err := pgxpool.New(ctx, cfg.databaseURL)
	if err != nil {
		return "", fmt.Errorf("platform database boundary: %w", err)
	}
	defer db.Close()

	if err := client.SubmitVerification(ctx); err != nil {
		return "", fmt.Errorf("verification boundary: %w", err)
	}
	from, to, err := client.CreateWalletPair(ctx)
	if err != nil {
		return "", fmt.Errorf("wallet boundary: %w", err)
	}
	if err := smoke.SetBalance(ctx, db, from, 500000); err != nil {
		return "", fmt.Errorf("wallet boundary: %w", err)
	}
	payment, err := client.StartPayment(ctx, from, to, 250000, http.StatusAccepted)
	if err != nil {
		return "", fmt.Errorf("transfer boundary: %w", err)
	}
	return payment.PaymentID, nil
}

type auditSession struct {
	Completed     bool
	FinalOutcome  string
	FailureReason string
	Sanitized     string
	Enrichment    string
	Events        string
	Verdict       string
	RawResponse   string
}

// checkAuditSession proves the worker persisted one durable, sanitized session.
func checkAuditSession(ctx context.Context, cfg config, paymentID string) error {
	db, err := pgxpool.New(ctx, cfg.fraudDatabaseURL)
	if err != nil {
		return fmt.Errorf("connect audit database: %w", err)
	}
	defer db.Close()

	waitCtx, cancel := context.WithTimeout(ctx, boundaryTimeout)
	defer cancel()
	var session auditSession
	err = smoke.Poll(waitCtx, func() (bool, error) {
		row := db.QueryRow(waitCtx, `
			SELECT completed_at IS NOT NULL, final_outcome, failure_reason,
			       sanitized_facts_json::text, enrichment_json::text,
			       events_json::text, parsed_verdict_json::text, raw_llm_response
			FROM fraud_sessions WHERE payment_id = $1`, paymentID)
		var current auditSession
		if scanErr := row.Scan(&current.Completed, &current.FinalOutcome, &current.FailureReason,
			&current.Sanitized, &current.Enrichment, &current.Events, &current.Verdict, &current.RawResponse); scanErr != nil {
			return false, nil
		}
		session = current
		return current.Completed, nil
	}, "fraud audit session for payment "+paymentID)
	if err != nil {
		return err
	}
	if session.FinalOutcome == "" {
		return fmt.Errorf("session has no recorded outcome")
	}
	if session.Sanitized == "{}" || session.Enrichment == "{}" {
		return fmt.Errorf("session is missing sanitized enrichment; facts=%s enrichment=%s", session.Sanitized, session.Enrichment)
	}
	for _, node := range []string{"create_session", "enrich_transaction", "build_prompt", "input_guard", "complete_session"} {
		if !strings.Contains(session.Events, node) {
			return fmt.Errorf("session events are missing node %s: %s", node, truncate(session.Events))
		}
	}
	if session.FinalOutcome != "fail_open" {
		if session.Verdict == "{}" {
			return fmt.Errorf("session outcome %s has no parsed verdict", session.FinalOutcome)
		}
		if session.RawResponse == "" {
			return fmt.Errorf("session outcome %s has no stored model response", session.FinalOutcome)
		}
	}
	// The payment ID is the one identifier an audit row may carry.
	stored := strings.ReplaceAll(
		strings.Join([]string{session.Sanitized, session.Enrichment, session.Events, session.Verdict}, " "),
		paymentID, "payment",
	)
	if uuidsIn(stored) {
		return fmt.Errorf("audit row leaked a raw identifier: %s", truncate(stored))
	}
	fmt.Fprintf(os.Stdout, "phase4 smoke: audit outcome=%s reason=%q\n", session.FinalOutcome, session.FailureReason)
	return nil
}

// checkPrometheus proves every scrape target is up and fraud series have data.
func checkPrometheus(ctx context.Context, cfg config) error {
	waitCtx, cancel := context.WithTimeout(ctx, boundaryTimeout)
	defer cancel()
	client := &http.Client{Timeout: 5 * time.Second}

	var down []string
	err := smoke.Poll(waitCtx, func() (bool, error) {
		var payload struct {
			Data struct {
				ActiveTargets []struct {
					Health string            `json:"health"`
					Labels map[string]string `json:"labels"`
				} `json:"activeTargets"`
			} `json:"data"`
		}
		if err := getJSON(waitCtx, client, cfg.prometheusURL+"/api/v1/targets?state=active", "", "", &payload); err != nil {
			return false, nil
		}
		if len(payload.Data.ActiveTargets) == 0 {
			return false, nil
		}
		down = down[:0]
		for _, target := range payload.Data.ActiveTargets {
			if target.Health != "up" {
				down = append(down, target.Labels["instance"]+"="+target.Health)
			}
		}
		return len(down) == 0, nil
	}, "healthy Prometheus targets")
	if err != nil {
		return fmt.Errorf("%w; unhealthy targets: %v", err, down)
	}

	for _, query := range []string{"fraud_session_duration_seconds_count", "fraud_enrichment_calls_total"} {
		if err := requireSeries(waitCtx, client, cfg.prometheusURL, query); err != nil {
			return err
		}
	}
	return nil
}

func requireSeries(ctx context.Context, client *http.Client, prometheusURL, query string) error {
	return smoke.Poll(ctx, func() (bool, error) {
		var payload struct {
			Data struct {
				Result []struct {
					Value []any `json:"value"`
				} `json:"result"`
			} `json:"data"`
		}
		if err := getJSON(ctx, client, prometheusURL+"/api/v1/query?query="+url.QueryEscape(query), "", "", &payload); err != nil {
			return false, nil
		}
		return len(payload.Data.Result) > 0, nil
	}, "Prometheus series "+query)
}

// checkTrace proves one trace crosses the saga, Kafka, and the Python worker.
func checkTrace(ctx context.Context, cfg config, paymentID string) error {
	waitCtx, cancel := context.WithTimeout(ctx, boundaryTimeout)
	defer cancel()
	client := &http.Client{Timeout: 5 * time.Second}
	query := fmt.Sprintf("%s/api/traces?service=%s&lookback=1h&limit=50", cfg.jaegerURL, fraudService)

	var seenServices []string
	err := smoke.Poll(waitCtx, func() (bool, error) {
		var payload struct {
			Data []struct {
				Processes map[string]struct {
					ServiceName string `json:"serviceName"`
				} `json:"processes"`
				Spans []struct {
					OperationName string `json:"operationName"`
				} `json:"spans"`
			} `json:"data"`
		}
		if err := getJSON(waitCtx, client, query, "", "", &payload); err != nil {
			return false, nil
		}
		for _, trace := range payload.Data {
			services := map[string]bool{}
			for _, process := range trace.Processes {
				services[process.ServiceName] = true
			}
			if !services[sagaService] || !services[fraudService] {
				continue
			}
			operations := map[string]bool{}
			for _, span := range trace.Spans {
				operations[span.OperationName] = true
			}
			if operations["fraud.worker.consume"] && operations["fraud.llm.complete"] {
				seenServices = keys(services)
				return true, nil
			}
		}
		return false, nil
	}, "one Jaeger trace crossing "+sagaService+" and "+fraudService)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "phase4 smoke: trace services %v\n", seenServices)
	return nil
}

// checkDashboards proves Grafana is healthy and the fraud dashboard is provisioned.
func checkDashboards(ctx context.Context, cfg config) error {
	waitCtx, cancel := context.WithTimeout(ctx, boundaryTimeout)
	defer cancel()
	client := &http.Client{Timeout: 5 * time.Second}

	var titles []string
	err := smoke.Poll(waitCtx, func() (bool, error) {
		var health struct {
			Database string `json:"database"`
		}
		if err := getJSON(waitCtx, client, cfg.grafanaURL+"/api/health", cfg.grafanaUser, cfg.grafanaPassword, &health); err != nil {
			return false, nil
		}
		if health.Database != "ok" {
			return false, nil
		}
		var dashboards []struct {
			Title string `json:"title"`
		}
		if err := getJSON(waitCtx, client, cfg.grafanaURL+"/api/search?type=dash-db", cfg.grafanaUser, cfg.grafanaPassword, &dashboards); err != nil {
			return false, nil
		}
		titles = titles[:0]
		for _, dashboard := range dashboards {
			titles = append(titles, dashboard.Title)
		}
		return len(dashboards) >= 3, nil
	}, "provisioned Grafana dashboards")
	if err != nil {
		return fmt.Errorf("%w; dashboards: %v", err, titles)
	}
	if !containsFold(titles, "fraud") {
		return fmt.Errorf("no fraud dashboard is provisioned; dashboards: %v", titles)
	}
	return nil
}

func getJSON(ctx context.Context, client *http.Client, endpoint, user, password string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	if user != "" {
		req.SetBasicAuth(user, password)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s status=%d", endpoint, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func uuidsIn(value string) bool {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return !(r == '-' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F'))
	})
	for _, field := range fields {
		if len(field) == 36 && strings.Count(field, "-") == 4 {
			return true
		}
	}
	return false
}

func keys(values map[string]bool) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	return result
}

func containsFold(values []string, want string) bool {
	for _, value := range values {
		if strings.Contains(strings.ToLower(value), want) {
			return true
		}
	}
	return false
}

func truncate(value string) string {
	if len(value) <= 300 {
		return value
	}
	return value[:300] + "..."
}
