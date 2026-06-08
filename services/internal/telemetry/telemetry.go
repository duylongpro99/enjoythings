package telemetry

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"

	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"
	"go.opentelemetry.io/otel/trace"
)

const (
	TraceparentHeader = "traceparent"
	TracestateHeader  = "tracestate"
)

var allowedAttributeKeys = map[string]struct{}{
	"service.name":              {},
	"messaging.kafka.topic":     {},
	"operation":                 {},
	"provider.id":               {},
	"model.id":                  {},
	"fraud.session_id":          {},
	"payment.id":                {},
	"verdict.action":            {},
	"outcome":                   {},
	"rpc.service":               {},
	"rpc.method":                {},
	"http.request.method":       {},
	"http.response.status_code": {},
}

func init() {
	otel.SetTextMapPropagator(propagation.TraceContext{})
}

func Init(ctx context.Context, serviceName string, appEnv string) (func(context.Context) error, error) {
	otel.SetTextMapPropagator(propagation.TraceContext{})
	if serviceName == "" {
		serviceName = "enjoythings-service"
	}
	res, err := resource.Merge(
		resource.Default(),
		resource.NewWithAttributes(semconv.SchemaURL, semconv.ServiceName(serviceName)),
	)
	if err != nil {
		return func(context.Context) error { return nil }, err
	}
	providerOptions := []sdktrace.TracerProviderOption{
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sampler(appEnv)),
	}
	if endpoint := strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")); endpoint != "" {
		exporter, err := otlptracehttp.New(ctx)
		if err == nil {
			providerOptions = append(providerOptions, sdktrace.WithBatcher(exporter))
		}
	}
	provider := sdktrace.NewTracerProvider(providerOptions...)
	otel.SetTracerProvider(provider)
	return provider.Shutdown, nil
}

func Tracer() trace.Tracer {
	return otel.Tracer("enjoythings/services")
}

func ExtractKafka(ctx context.Context, record *kgo.Record) context.Context {
	return propagation.TraceContext{}.Extract(ctx, kafkaCarrier{record: record})
}

func InjectKafka(ctx context.Context, record *kgo.Record) {
	propagation.TraceContext{}.Inject(ctx, kafkaCarrier{record: record})
}

func ExtractTextMap(ctx context.Context, carrier propagation.TextMapCarrier) context.Context {
	return propagation.TraceContext{}.Extract(ctx, carrier)
}

func InjectTextMap(ctx context.Context, carrier propagation.TextMapCarrier) {
	propagation.TraceContext{}.Inject(ctx, carrier)
}

func SafeAttributes(keyValues ...string) []attribute.KeyValue {
	attrs := make([]attribute.KeyValue, 0, len(keyValues)/2)
	for i := 0; i+1 < len(keyValues); i += 2 {
		key := strings.TrimSpace(keyValues[i])
		if !isAllowedAttribute(key) {
			continue
		}
		attrs = append(attrs, attribute.String(key, bounded(keyValues[i+1], 128)))
	}
	return attrs
}

func RecordError(span trace.Span, err error) {
	if err == nil {
		return
	}
	span.RecordError(errors.New(boundedErrorType(err)))
	span.SetStatus(codes.Error, boundedErrorType(err))
}

func CurrentTraceparent(ctx context.Context) string {
	carrier := propagation.MapCarrier{}
	otel.GetTextMapPropagator().Inject(ctx, carrier)
	return carrier.Get(TraceparentHeader)
}

func sampler(appEnv string) sdktrace.Sampler {
	if strings.EqualFold(appEnv, "local") || strings.EqualFold(appEnv, "dev") || appEnv == "" {
		return sdktrace.AlwaysSample()
	}
	ratio := 0.1
	if raw := os.Getenv("OTEL_TRACES_SAMPLER_ARG"); raw != "" {
		if parsed, err := strconv.ParseFloat(raw, 64); err == nil && parsed >= 0 && parsed <= 1 {
			ratio = parsed
		}
	}
	return sdktrace.ParentBased(sdktrace.TraceIDRatioBased(ratio))
}

func isAllowedAttribute(key string) bool {
	if key == "" {
		return false
	}
	_, allowed := allowedAttributeKeys[key]
	return allowed
}

func bounded(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}

func boundedErrorType(err error) string {
	name := "error"
	if err != nil {
		name = strings.TrimPrefix(strings.TrimPrefix(strings.SplitN(err.Error(), ":", 2)[0], "*"), ".")
	}
	return bounded(name, 64)
}

type kafkaCarrier struct {
	record *kgo.Record
}

func (carrier kafkaCarrier) Get(key string) string {
	for _, header := range carrier.record.Headers {
		if strings.EqualFold(header.Key, key) {
			return string(header.Value)
		}
	}
	return ""
}

func (carrier kafkaCarrier) Set(key string, value string) {
	for i, header := range carrier.record.Headers {
		if strings.EqualFold(header.Key, key) {
			carrier.record.Headers[i].Value = []byte(value)
			return
		}
	}
	carrier.record.Headers = append(carrier.record.Headers, kgo.RecordHeader{Key: key, Value: []byte(value)})
}

func (carrier kafkaCarrier) Keys() []string {
	keys := make([]string, 0, len(carrier.record.Headers))
	for _, header := range carrier.record.Headers {
		keys = append(keys, header.Key)
	}
	return keys
}
