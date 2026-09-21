package repository

import (
	"context"
	"errors"
	"testing"

	"example.com/rapira_rates/internal/model/rate"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/puddle/v2"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func TestSaveTracingFailure(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
		if err := provider.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	pool, err := pgxpool.New(context.Background(), "postgres://unused:unused@localhost:1/unused?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	pool.Close()
	ctx, parent := provider.Tracer("test").Start(context.Background(), "GetRates")
	defer parent.End()
	err = NewRateRepository(pool).Save(ctx, rate.Result{
		Symbol: "USDT/RUB",
		Params: rate.CalculationParams{Method: rate.MethodTopN, N: 1},
	})
	if !errors.Is(err, puddle.ErrClosedPool) {
		t.Fatalf("database error was not preserved: %v", err)
	}
	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("want one completed child span, got %d", len(spans))
	}
	span := spans[0]
	if span.Name() != "postgres.SaveRate" || span.SpanKind() != trace.SpanKindClient || span.Parent().SpanID() != parent.SpanContext().SpanID() || span.SpanContext().TraceID() != parent.SpanContext().TraceID() {
		t.Fatalf("incorrect database span: %v", span)
	}
	if span.Status().Code != codes.Error || len(span.Events()) != 1 || span.Events()[0].Name != "exception" {
		t.Fatalf("missing error status or exception: %v, %v", span.Status(), span.Events())
	}
	found := false
	for _, attr := range span.Events()[0].Attributes {
		if attr.Key == "exception.message" && attr.Value.AsString() == err.Error() {
			found = true
		}
	}
	if !found {
		t.Fatal("returned error was not recorded")
	}
	attrs := make(map[string]string)
	for _, attr := range span.Attributes() {
		attrs[string(attr.Key)] = attr.Value.AsString()
	}
	for key, want := range map[string]string{
		"db.system.name": "postgresql", "db.collection.name": "rate_results",
		"db.operation.name": "INSERT", "rates.symbol": "USDT/RUB", "rates.method": "topN",
	} {
		if attrs[key] != want {
			t.Errorf("attribute %s = %q, want %q", key, attrs[key], want)
		}
	}
}
