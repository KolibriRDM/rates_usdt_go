package rapira

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

func TestGetOrderBookTracing(t *testing.T) {
	for _, tt := range []struct {
		name     string
		body     string
		status   int
		symbol   string
		canceled bool
		wantErr  bool
	}{
		{name: "success", body: validDepth, status: http.StatusOK, symbol: "USDT/RUB"},
		{name: "HTTP error", status: http.StatusServiceUnavailable, symbol: "USDT/RUB", wantErr: true},
		{name: "invalid JSON", body: "{", status: http.StatusOK, symbol: "USDT/RUB", wantErr: true},
		{name: "invalid order book", body: "{}", status: http.StatusOK, symbol: "USDT/RUB", wantErr: true},
		{name: "empty symbol", wantErr: true},
		{name: "canceled context", symbol: "USDT/RUB", canceled: true, wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
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
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()
			ctx, parent := provider.Tracer("test").Start(context.Background(), "GetRates")
			defer parent.End()
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			if tt.canceled {
				cancel()
			}
			_, err := NewClient(server.URL, time.Second).GetOrderBook(ctx, tt.symbol)
			if (err != nil) != tt.wantErr {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.canceled && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation was not preserved: %v", err)
			}
			spans := recorder.Ended()
			if len(spans) != 1 {
				t.Fatalf("want one completed child span, got %d", len(spans))
			}
			span := spans[0]
			if span.Name() != "rapira.GetOrderBook" || span.SpanKind() != trace.SpanKindClient || span.Parent().SpanID() != parent.SpanContext().SpanID() || span.SpanContext().TraceID() != parent.SpanContext().TraceID() {
				t.Fatalf("incorrect child span: %v", span)
			}
			if (span.Status().Code == codes.Error) != tt.wantErr {
				t.Fatalf("unexpected span status: %v", span.Status())
			}
			if tt.wantErr {
				if len(span.Events()) != 1 || span.Events()[0].Name != "exception" {
					t.Fatalf("missing exception event: %v", span.Events())
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
			} else if len(span.Events()) != 0 {
				t.Fatalf("unexpected success events: %v", span.Events())
			}
			attrs := make(map[string]any)
			for _, attr := range span.Attributes() {
				attrs[string(attr.Key)] = attr.Value.AsInterface()
			}
			if attrs["rates.symbol"] != tt.symbol || attrs["http.request.method"] != "POST" {
				t.Fatalf("missing request attributes: %v", attrs)
			}
			if (tt.name == "success" || tt.name == "HTTP error" || tt.name == "invalid order book") && attrs["http.response.status_code"] != int64(tt.status) {
				t.Fatalf("missing HTTP status: %v", attrs)
			}
		})
	}
}
