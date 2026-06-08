package repo

import (
	"context"
	"time"

	"enjoythings/services/internal/telemetry"

	"github.com/jackc/pgx/v5"
	"go.opentelemetry.io/otel/trace"
)

type queryTraceKey struct{}

type queryTrace struct {
	span    trace.Span
	started time.Time
}

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
	return context.WithValue(ctx, queryTraceKey{}, queryTrace{span: span, started: time.Now()})
}

func (pgxQueryTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	query, ok := ctx.Value(queryTraceKey{}).(queryTrace)
	if !ok {
		return
	}
	outcome := "success"
	if data.Err != nil {
		outcome = "failure"
		telemetry.RecordError(query.span, data.Err)
	}
	telemetry.ServiceMetrics("database").RecordDB("query", outcome, time.Since(query.started))
	query.span.End()
}
