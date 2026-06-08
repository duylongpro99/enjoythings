package telemetry

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestMetricsHandlerExposesBoundedHTTPMetrics(t *testing.T) {
	registry := prometheus.NewRegistry()
	metrics := NewMetrics("wallet", registry)
	handler := metrics.HTTPMiddleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))

	request := httptest.NewRequest(http.MethodPost, "/v1/wallets/private-id", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	metricsResponse := httptest.NewRecorder()
	metrics.Handler().ServeHTTP(metricsResponse, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := metricsResponse.Body.String()
	if !strings.Contains(body, `service_http_requests_total{method="POST",route="/v1/wallets/:id",service="wallet",status="201"} 1`) {
		t.Fatalf("metrics body missing bounded request metric:\n%s", body)
	}
	if strings.Contains(body, "private-id") {
		t.Fatalf("metrics contain request identifier:\n%s", body)
	}
}

func TestMetricsRejectUnknownKafkaTopics(t *testing.T) {
	metrics := NewMetrics("saga-orchestrator", prometheus.NewRegistry())
	if metrics.RecordKafka("private.topic", "produced") {
		t.Fatal("unknown Kafka topic must not become a label")
	}
	if !metrics.RecordKafka("fraud.flagged", "consumed") {
		t.Fatal("known bounded Kafka topic was rejected")
	}
}
