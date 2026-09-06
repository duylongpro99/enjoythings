package telemetry

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

var boundedKafkaTopics = buildBoundedKafkaTopics()

var boundedSourceTopics = []string{
	"tx.initiated", "payment.execute", "payment.completed", "payment.failed",
	"tx.completed", "tx.failed", "tx.paused", "fraud.score.requested",
	"fraud.flagged", "fraud.error", "user.verified", "user.rejected",
}

// buildBoundedKafkaTopics admits every source topic and its dead-letter topic,
// so parked poison records stay visible in the same metric as live traffic.
func buildBoundedKafkaTopics() map[string]struct{} {
	topics := make(map[string]struct{}, len(boundedSourceTopics)*2)
	for _, topic := range boundedSourceTopics {
		topics[topic] = struct{}{}
		topics[topic+".dlq"] = struct{}{}
	}
	return topics
}

type Metrics struct {
	service      string
	registry     prometheus.Gatherer
	httpRequests *prometheus.CounterVec
	httpLatency  *prometheus.HistogramVec
	grpcRequests *prometheus.CounterVec
	grpcLatency  *prometheus.HistogramVec
	kafkaRecords *prometheus.CounterVec
	sagaDuration *prometheus.HistogramVec
	sagaEvents   *prometheus.CounterVec
	sagaStates   *prometheus.CounterVec
	dbLatency    *prometheus.HistogramVec
}

func NewMetrics(service string, registerer prometheus.Registerer) *Metrics {
	metrics := &Metrics{
		service:      service,
		httpRequests: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "service_http_requests_total", Help: "HTTP requests by bounded route."}, []string{"service", "method", "route", "status"}),
		httpLatency:  prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "service_http_request_duration_seconds", Help: "HTTP request latency.", Buckets: prometheus.DefBuckets}, []string{"service", "method", "route"}),
		grpcRequests: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "service_grpc_requests_total", Help: "gRPC requests by bounded method and status."}, []string{"service", "method", "status"}),
		grpcLatency:  prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "service_grpc_request_duration_seconds", Help: "gRPC request latency.", Buckets: prometheus.DefBuckets}, []string{"service", "method"}),
		kafkaRecords: prometheus.NewCounterVec(prometheus.CounterOpts{Name: "service_kafka_records_total", Help: "Kafka records by topic and outcome."}, []string{"service", "topic", "outcome"}),
		sagaDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "saga_duration_seconds", Help: "Saga duration.", Buckets: []float64{0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60}}, []string{"outcome"}),
		sagaEvents:   prometheus.NewCounterVec(prometheus.CounterOpts{Name: "saga_events_total", Help: "Saga failures, compensations, and fraud reviews."}, []string{"event"}),
		sagaStates:   prometheus.NewCounterVec(prometheus.CounterOpts{Name: "saga_state_transitions_total", Help: "Saga transitions into bounded states."}, []string{"state"}),
		dbLatency:    prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "service_database_operation_duration_seconds", Help: "Database operation latency.", Buckets: prometheus.DefBuckets}, []string{"service", "operation", "outcome"}),
	}
	for _, collector := range []prometheus.Collector{metrics.httpRequests, metrics.httpLatency, metrics.grpcRequests, metrics.grpcLatency, metrics.kafkaRecords, metrics.sagaDuration, metrics.sagaEvents, metrics.sagaStates, metrics.dbLatency} {
		registerer.MustRegister(collector)
	}
	if gatherer, ok := registerer.(prometheus.Gatherer); ok {
		metrics.registry = gatherer
	} else {
		metrics.registry = prometheus.DefaultGatherer
	}
	return metrics
}

func (metrics *Metrics) RecordSagaState(state string) bool {
	if !oneOf(state, "STARTED", "VERIFICATION_CHECKED", "WALLET_DEBITED", "LEDGER_RESERVED", "PAYMENT_PROCESSING", "FRAUD_REVIEW", "LEDGER_CONFIRMED", "COMPLETED", "COMPENSATING_LEDGER", "COMPENSATING_WALLET", "FAILED") {
		return false
	}
	metrics.sagaStates.WithLabelValues(state).Inc()
	return true
}

func (metrics *Metrics) UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, request any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		started := time.Now()
		response, err := handler(ctx, request)
		metrics.grpcRequests.WithLabelValues(metrics.service, info.FullMethod, status.Code(err).String()).Inc()
		metrics.grpcLatency.WithLabelValues(metrics.service, info.FullMethod).Observe(time.Since(started).Seconds())
		return response, err
	}
}

func (metrics *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(metrics.registry, promhttp.HandlerOpts{})
}

func (metrics *Metrics) HTTPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		started := time.Now()
		response := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(response, request)
		route := boundedRoute(request.URL.Path)
		metrics.httpRequests.WithLabelValues(metrics.service, request.Method, route, strconv.Itoa(response.status)).Inc()
		metrics.httpLatency.WithLabelValues(metrics.service, request.Method, route).Observe(time.Since(started).Seconds())
	})
}

func (metrics *Metrics) RecordKafka(topic, outcome string) bool {
	if _, ok := boundedKafkaTopics[topic]; !ok || !oneOf(outcome, "consumed", "produced", "failed", "retried") {
		return false
	}
	metrics.kafkaRecords.WithLabelValues(metrics.service, topic, outcome).Inc()
	return true
}

func (metrics *Metrics) RecordSaga(event string, duration time.Duration) bool {
	if oneOf(event, "completed", "failed") {
		metrics.sagaDuration.WithLabelValues(event).Observe(duration.Seconds())
		return true
	}
	if oneOf(event, "step_failure", "compensation", "fraud_review", "fraud_review_resumed", "fraud_review_rejected", "fraud_review_expired") {
		metrics.sagaEvents.WithLabelValues(event).Inc()
		return true
	}
	return false
}

func (metrics *Metrics) RecordDB(operation, outcome string, duration time.Duration) bool {
	if !oneOf(operation, "query", "exec", "transaction") || !oneOf(outcome, "success", "failure") {
		return false
	}
	metrics.dbLatency.WithLabelValues(metrics.service, operation, outcome).Observe(duration.Seconds())
	return true
}

var (
	defaultMetrics   sync.Map
	defaultMetricsMu sync.Mutex
	defaultBase      *Metrics
)

func ServiceMetrics(service string) *Metrics {
	if existing, ok := defaultMetrics.Load(service); ok {
		return existing.(*Metrics)
	}
	defaultMetricsMu.Lock()
	defer defaultMetricsMu.Unlock()
	if existing, ok := defaultMetrics.Load(service); ok {
		return existing.(*Metrics)
	}
	if defaultBase == nil {
		defaultBase = NewMetrics(service, prometheus.DefaultRegisterer)
		defaultMetrics.Store(service, defaultBase)
		return defaultBase
	}
	metrics := *defaultBase
	metrics.service = service
	defaultMetrics.Store(service, &metrics)
	return &metrics
}

func InstrumentHTTP(service string, mux *http.ServeMux) http.Handler {
	metrics := ServiceMetrics(service)
	mux.Handle("/metrics", metrics.Handler())
	return metrics.HTTPMiddleware(mux)
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (writer *statusWriter) Unwrap() http.ResponseWriter {
	return writer.ResponseWriter
}

func (writer *statusWriter) WriteHeader(status int) {
	writer.status = status
	writer.ResponseWriter.WriteHeader(status)
}

func boundedRoute(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	for index, part := range parts {
		if index > 0 && (looksLikeID(part) || parts[index-1] == "payments" || parts[index-1] == "wallets") {
			parts[index] = ":id"
		}
	}
	if len(parts) == 1 && parts[0] == "" {
		return "/"
	}
	return "/" + strings.Join(parts, "/")
}

func looksLikeID(value string) bool {
	return len(value) > 24 || strings.Count(value, "-") >= 2
}

func oneOf(value string, allowed ...string) bool {
	for _, item := range allowed {
		if value == item {
			return true
		}
	}
	return false
}
