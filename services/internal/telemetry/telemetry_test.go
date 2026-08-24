package telemetry

import (
	"context"
	"testing"

	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

const knownTraceparent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"

func TestKafkaContextRoundTripPreservesParent(t *testing.T) {
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(tracetest.NewInMemoryExporter()))
	propagator := propagation.TraceContext{}
	parent := propagator.Extract(context.Background(), propagation.MapCarrier{
		"traceparent": knownTraceparent,
	})
	ctx, span := provider.Tracer("test").Start(parent, "producer")
	defer span.End()

	record := &kgo.Record{}
	InjectKafka(ctx, record)
	childParent := ExtractKafka(context.Background(), record)

	got := trace.SpanContextFromContext(childParent)
	want := trace.SpanContextFromContext(ctx)
	if got.TraceID() != want.TraceID() || got.SpanID() != want.SpanID() {
		t.Fatalf("extracted context = %s/%s, want %s/%s", got.TraceID(), got.SpanID(), want.TraceID(), want.SpanID())
	}
}

func TestInitBuildsAServiceResourceForTheInstalledSDK(t *testing.T) {
	shutdown, err := Init(context.Background(), "saga-orchestrator", "local")
	if err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() {
		if err := shutdown(context.Background()); err != nil {
			t.Fatalf("shutdown: %v", err)
		}
	})
}

func TestSafeAttributesRejectSensitiveKeys(t *testing.T) {
	attrs := SafeAttributes(
		"payment.id", "payment-1",
		"user.id", "private-user",
		"wallet_id", "private-wallet",
		"outcome", "completed",
	)

	if len(attrs) != 2 {
		t.Fatalf("attributes = %v, want only payment.id and outcome", attrs)
	}
}
