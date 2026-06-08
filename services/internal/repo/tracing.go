package repo

import (
	"context"

	"enjoythings/services/internal/telemetry"

	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel/trace"
)

type queryTraceKey struct{}

type pgxQueryTracer struct{}

func (pgxQueryTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	operation := "query"
	if data.SQL != "" {
		operation = "query"
	}
	ctx, span := telemetry.Tracer().Start(
		ctx,
		"db.query",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(telemetry.SafeAttributes("operation", operation)...),
	)
	return context.WithValue(ctx, queryTraceKey{}, span)
}

func (pgxQueryTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	span, ok := ctx.Value(queryTraceKey{}).(trace.Span)
	if !ok {
		return
	}
	if data.Err != nil {
		telemetry.RecordError(span, data.Err)
	}
	span.End()
}
