package devtools

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPhase4ComposeDefinesObservableRuntimeWithoutLiteralCredentials(t *testing.T) {
	root := repoRoot(t)
	compose := readText(t, filepath.Join(root, "docker-compose.yml"))

	for _, snippet := range []string{
		"fraud-timescaledb:",
		"fraud-worker:",
		"prometheus:",
		"grafana:",
		"prometheus-data:",
		"grafana-data:",
		"fraud-timescaledb-data:",
		"--storage.tsdb.retention.time=7d",
		"GF_SECURITY_ADMIN_USER: ${GRAFANA_ADMIN_USER}",
		"GF_SECURITY_ADMIN_PASSWORD: ${GRAFANA_ADMIN_PASSWORD}",
		"FRAUD_DATABASE_URL: postgres://${FRAUD_DB_USER}:${FRAUD_DB_PASSWORD}@fraud-timescaledb:5432/${FRAUD_DB_NAME}",
	} {
		requireContains(t, compose, snippet)
	}
	for _, forbidden := range []string{"image: prom/prometheus:latest", "image: grafana/grafana:latest", "GF_AUTH_ANONYMOUS_ENABLED: \"true\""} {
		if strings.Contains(compose, forbidden) {
			t.Fatalf("compose contains forbidden runtime setting %q", forbidden)
		}
	}
}

func TestPhase4PrometheusAndGrafanaProvisioningContracts(t *testing.T) {
	root := repoRoot(t)
	prometheusConfig := readText(t, filepath.Join(root, "observability", "prometheus", "prometheus.yml"))
	for _, target := range []string{
		"gateway:8080", "wallet:8080", "ledger:8080", "saga-orchestrator:8080",
		"payment-processor:8080", "verification:8080", "notification:8080", "fraud-worker:9101",
	} {
		requireContains(t, prometheusConfig, target)
	}

	dashboardDir := filepath.Join(root, "observability", "grafana", "dashboards")
	entries, err := os.ReadDir(dashboardDir)
	if err != nil {
		t.Fatalf("read dashboards: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("dashboard count = %d, want 3", len(entries))
	}
	for _, entry := range entries {
		data := readText(t, filepath.Join(dashboardDir, entry.Name()))
		var dashboard map[string]any
		if err := json.Unmarshal([]byte(data), &dashboard); err != nil {
			t.Fatalf("parse dashboard %s: %v", entry.Name(), err)
		}
		if strings.Contains(strings.ToLower(data), "timescale") {
			t.Fatalf("dashboard %s must query Prometheus only", entry.Name())
		}
		requireContains(t, data, `"datasource": {"type": "prometheus", "uid": "prometheus"}`)
	}
}

func TestPhase4HelmValuesExposeMetricsAndFraudRuntime(t *testing.T) {
	root := repoRoot(t)
	values := readText(t, filepath.Join(root, "charts", "enjoythings", "values.yaml"))
	applications := readText(t, filepath.Join(root, "charts", "enjoythings", "templates", "applications.yaml"))
	services := readText(t, filepath.Join(root, "charts", "enjoythings", "templates", "services.yaml"))

	for _, snippet := range []string{"fraud-worker:", "fraudTimescaledb:", "metricsPort: 9101", "fraudDatabaseUrl:"} {
		requireContains(t, values, snippet)
	}
	requireContains(t, applications, "prometheus.io/scrape")
	requireContains(t, applications, "prometheus.io/port")
	requireContains(t, services, "name: metrics")
}
