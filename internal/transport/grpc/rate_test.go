package grpcserver_test

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	ratev1 "example.com/rapira_rates/gen/rate/v1"
	"example.com/rapira_rates/internal/client/rapira"
	"example.com/rapira_rates/internal/repository"
	"example.com/rapira_rates/internal/service"
	grpcserver "example.com/rapira_rates/internal/transport/grpc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel"
	otelcodes "go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestGetRatesInvalidParameters(t *testing.T) {
	const topN = ratev1.CalculationMethod_CALCULATION_METHOD_TOP_N
	const avgNM = ratev1.CalculationMethod_CALCULATION_METHOD_AVG_NM
	tests := []struct {
		name string
		req  *ratev1.GetRatesRequest
		want string
	}{
		{name: "nil request", want: "unsupported calculation method"},
		{name: "unspecified method", req: &ratev1.GetRatesRequest{N: 1}, want: "unsupported calculation method"},
		{name: "unknown method", req: &ratev1.GetRatesRequest{Method: 42, N: 1}, want: "unsupported calculation method"},
		{name: "negative enum", req: &ratev1.GetRatesRequest{Method: -1, N: 1}, want: "unsupported calculation method"},
		{name: "zero n", req: &ratev1.GetRatesRequest{Method: topN}, want: "positions must be greater than zero"},
		{name: "unexpected m", req: &ratev1.GetRatesRequest{Method: topN, N: 1, M: 2}, want: "m must be zero for topN"},
		{name: "zero m", req: &ratev1.GetRatesRequest{Method: avgNM, N: 1}, want: "positions must be greater than zero"},
		{name: "reversed range", req: &ratev1.GetRatesRequest{Method: avgNM, N: 2, M: 1}, want: "m must not be less than n"},
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	handler := grpcserver.NewRateHandler(service.NewRateService(nil, nil, "USDT/RUB"), logger)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response, err := handler.GetRates(context.Background(), tt.req)
			if response != nil || status.Code(err) != codes.InvalidArgument || status.Convert(err).Message() != tt.want {
				t.Fatalf("response=%v, error=%v, want InvalidArgument: %s", response, err, tt.want)
			}
		})
	}
}

func TestGetRatesPersistenceIntegration(t *testing.T) {
	dsn := os.Getenv("RAPIRA_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set RAPIRA_TEST_DATABASE_URL to run the PostgreSQL integration test")
	}
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
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := fmt.Sprintf("handler_test_%d", time.Now().UnixNano())
	quotedSchema := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+quotedSchema); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		if _, err := admin.Exec(cleanupCtx, "DROP SCHEMA "+quotedSchema+" CASCADE"); err != nil {
			t.Errorf("clean up test schema: %v", err)
		}
	}()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	migration, err := os.ReadFile("../../../migrations/001_create_rate_results.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(strings.Replace(depth, `{"price":88.36,"amount":120}`, `{"price":88.36,"amount":120},{"price":88.34,"amount":200}`, 1)))
	}))
	defer server.Close()
	handler := grpcserver.NewRateHandler(
		service.NewRateService(repository.NewRateRepository(pool), rapira.NewClient(server.URL, time.Second), "USDT/RUB"),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	tests := []struct {
		req      *ratev1.GetRatesRequest
		ask, bid string
		method   string
	}{
		{req: &ratev1.GetRatesRequest{Method: ratev1.CalculationMethod_CALCULATION_METHOD_TOP_N, N: 2}, ask: "88.38", bid: "88.34", method: "topN"},
		{req: &ratev1.GetRatesRequest{Method: ratev1.CalculationMethod_CALCULATION_METHOD_AVG_NM, N: 1, M: 2}, ask: "88.375", bid: "88.35", method: "avgNM"},
		{req: &ratev1.GetRatesRequest{Method: ratev1.CalculationMethod_CALCULATION_METHOD_TOP_N, N: 2}, ask: "88.38", bid: "88.34", method: "topN"},
	}
	for i, tt := range tests {
		before := time.Now()
		requestCtx, parent := provider.Tracer("test").Start(ctx, "GetRates")
		response, err := handler.GetRates(requestCtx, tt.req)
		parent.End()
		if err != nil {
			t.Fatal(err)
		}
		spans := recorder.Ended()
		if len(spans) != (i+1)*3 {
			t.Fatalf("want two child spans and a parent per request, got %d spans", len(spans))
		}
		for j, name := range []string{"rapira.GetOrderBook", "postgres.SaveRate"} {
			span := spans[i*3+j]
			if span.Name() != name || span.Parent().SpanID() != parent.SpanContext().SpanID() || span.SpanContext().TraceID() != parent.SpanContext().TraceID() {
				t.Fatalf("incorrect child span: %v", span)
			}
			if span.Status().Code == otelcodes.Error || len(span.Events()) != 0 {
				t.Fatalf("unexpected error on successful operation: %v", span)
			}
		}
		result := response.GetResult()
		if result == nil || result.GetSymbol() != "USDT/RUB" || result.GetAsk() != tt.ask || result.GetBid() != tt.bid || result.GetMethod() != tt.req.GetMethod() || result.GetN() != tt.req.GetN() || result.GetM() != tt.req.GetM() {
			t.Fatalf("unexpected response: %v", response)
		}
		if err := result.GetReceivedAt().CheckValid(); err != nil {
			t.Fatal(err)
		}
		receivedAt := result.GetReceivedAt().AsTime()
		if receivedAt.Before(before) || receivedAt.After(time.Now()) {
			t.Fatalf("unexpected timestamp: %s", receivedAt)
		}
		var symbol, ask, bid, method string
		var storedAt time.Time
		var n, count int
		var m *int
		err = pool.QueryRow(ctx, "SELECT symbol, ask::text, bid::text, received_at, method, n, m FROM rate_results ORDER BY id DESC LIMIT 1").Scan(&symbol, &ask, &bid, &storedAt, &method, &n, &m)
		if err != nil {
			t.Fatal(err)
		}
		if symbol != result.GetSymbol() || ask != tt.ask || bid != tt.bid || method != tt.method || n != int(tt.req.GetN()) || !storedAt.Equal(receivedAt.Truncate(time.Microsecond)) {
			t.Fatalf("stored result differs: %s %s %s %s %s %d", symbol, ask, bid, storedAt, method, n)
		}
		if (tt.req.GetM() == 0 && m != nil) || (tt.req.GetM() != 0 && (m == nil || *m != int(tt.req.GetM()))) {
			t.Fatalf("unexpected stored m: %v", m)
		}
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM rate_results").Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != i+1 {
			t.Fatalf("expected one saved row per call, got %d", count)
		}
	}
}

const depth = `{"ask":{"symbol":"USDT/RUB","items":[{"price":88.37,"amount":150},{"price":88.38,"amount":200}]},"bid":{"symbol":"USDT/RUB","items":[{"price":88.36,"amount":120}]}}`

func TestGetRatesInsufficientLevels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(depth))
	}))
	defer server.Close()
	handler := grpcserver.NewRateHandler(
		service.NewRateService(nil, rapira.NewClient(server.URL, time.Second), "USDT/RUB"),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	for _, req := range []*ratev1.GetRatesRequest{
		{Method: ratev1.CalculationMethod_CALCULATION_METHOD_TOP_N, N: 3},
		{Method: ratev1.CalculationMethod_CALCULATION_METHOD_TOP_N, N: 2},
		{Method: ratev1.CalculationMethod_CALCULATION_METHOD_AVG_NM, N: 1, M: 2},
		{Method: ratev1.CalculationMethod_CALCULATION_METHOD_AVG_NM, N: 1, M: 3},
	} {
		response, err := handler.GetRates(context.Background(), req)
		if response != nil || status.Code(err) != codes.FailedPrecondition {
			t.Fatalf("request=%v, response=%v, error=%v", req, response, err)
		}
	}
}

func TestGetRatesContextErrors(t *testing.T) {
	handler := grpcserver.NewRateHandler(
		service.NewRateService(nil, rapira.NewClient("http://unused.invalid", time.Second), "USDT/RUB"),
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	expired, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer stop()
	for _, tt := range []struct {
		ctx  context.Context
		want codes.Code
	}{
		{ctx: canceled, want: codes.Canceled},
		{ctx: expired, want: codes.DeadlineExceeded},
	} {
		response, err := handler.GetRates(tt.ctx, &ratev1.GetRatesRequest{Method: ratev1.CalculationMethod_CALCULATION_METHOD_TOP_N, N: 1})
		if response != nil || status.Code(err) != tt.want {
			t.Fatalf("response=%v, error=%v, want %s", response, err, tt.want)
		}
	}
}

func TestGetRatesInternalError(t *testing.T) {
	for _, databaseFailure := range []bool{false, true} {
		name := "exchange failure"
		if databaseFailure {
			name = "database failure"
		}
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if !databaseFailure {
					http.Error(w, "private upstream details", http.StatusServiceUnavailable)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(depth))
			}))
			defer server.Close()
			var repo *repository.RateRepository
			if databaseFailure {
				pool, err := pgxpool.New(context.Background(), "postgres://unused:unused@localhost:1/unused?sslmode=disable")
				if err != nil {
					t.Fatal(err)
				}
				pool.Close()
				repo = repository.NewRateRepository(pool)
			}
			var logs bytes.Buffer
			handler := grpcserver.NewRateHandler(
				service.NewRateService(repo, rapira.NewClient(server.URL, time.Second), "USDT/RUB"),
				slog.New(slog.NewTextHandler(&logs, nil)),
			)
			response, err := handler.GetRates(context.Background(), &ratev1.GetRatesRequest{Method: ratev1.CalculationMethod_CALCULATION_METHOD_TOP_N, N: 1})
			if response != nil || status.Code(err) != codes.Internal || status.Convert(err).Message() != "failed to get rates" {
				t.Fatalf("response=%v, error=%v", response, err)
			}
			if !strings.Contains(logs.String(), "ERROR") || !strings.Contains(logs.String(), "error=") {
				t.Fatalf("missing internal error log: %s", logs.String())
			}
		})
	}
}
