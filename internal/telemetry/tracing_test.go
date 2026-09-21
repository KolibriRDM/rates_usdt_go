package telemetry

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"
)

func TestTracerProviderShutdownExportsPendingSpans(t *testing.T) {
	requests := make(chan *collectortrace.ExportTraceServiceRequest, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/traces" {
			t.Errorf("unexpected export request: %s %s", r.Method, r.URL.Path)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var request collectortrace.ExportTraceServiceRequest
		if err := proto.Unmarshal(body, &request); err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		select {
		case requests <- &request:
		default:
			t.Error("unexpected extra export request")
		}
		w.Header().Set("Content-Type", "application/x-protobuf")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", server.URL)
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
	t.Setenv("OTEL_EXPORTER_OTLP_COMPRESSION", "none")
	t.Setenv("OTEL_EXPORTER_OTLP_TRACES_COMPRESSION", "none")
	t.Setenv("OTEL_BSP_SCHEDULE_DELAY", "60000")
	provider, err := NewTracerProvider(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := provider.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	_, span := provider.Tracer("test").Start(context.Background(), "pending.operation")
	span.End()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := provider.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case request := <-requests:
		if len(request.ResourceSpans) != 1 {
			t.Fatalf("unexpected resource spans: %v", request.ResourceSpans)
		}
		resource := request.ResourceSpans[0]
		serviceName := ""
		for _, attr := range resource.Resource.Attributes {
			if attr.Key == "service.name" {
				serviceName = attr.Value.GetStringValue()
			}
		}
		if serviceName != "rapira_rates" {
			t.Fatalf("unexpected service name: %s", serviceName)
		}
		if len(resource.ScopeSpans) != 1 || len(resource.ScopeSpans[0].Spans) != 1 || resource.ScopeSpans[0].Spans[0].Name != "pending.operation" {
			t.Fatalf("pending span was not exported: %v", resource.ScopeSpans)
		}
	case <-ctx.Done():
		t.Fatal("shutdown did not export the pending span")
	}
}
