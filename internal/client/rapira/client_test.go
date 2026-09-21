package rapira

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const validDepth = `{
	"ask":{"symbol":"USDT/RUB","items":[
		{"price":88.370000000000000001,"amount":150.25},
		{"price":88.38,"amount":200}
	]},
	"bid":{"symbol":"USDT/RUB","items":[
		{"price":88.36,"amount":120.5},
		{"price":88.35,"amount":300}
	]}
}`

func TestGetOrderBook(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/market/exchange-plate-mini" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Accept") != "application/json" {
			t.Errorf("unexpected Accept: %q", r.Header.Get("Accept"))
		}
		if err := r.ParseMultipartForm(1024); err != nil {
			t.Errorf("parse multipart form: %v", err)
			http.Error(w, "invalid form", http.StatusBadRequest)
			return
		}
		if got := r.FormValue("symbol"); got != "USDT/RUB" {
			t.Errorf("unexpected symbol: %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(validDepth))
	}))
	defer server.Close()

	client := NewClient(server.URL, time.Second)
	before := time.Now()
	book, err := client.GetOrderBook(context.Background(), "USDT/RUB")
	after := time.Now()
	if err != nil {
		t.Fatal(err)
	}
	if book.Symbol != "USDT/RUB" || len(book.Asks) != 2 || len(book.Bids) != 2 {
		t.Fatalf("unexpected order book: %+v", book)
	}
	if book.Asks[0].Price.String() != "88.370000000000000001" || book.Asks[0].Amount.String() != "150.25" {
		t.Fatalf("ask precision lost: %+v", book.Asks[0])
	}
	if book.Asks[1].Price.String() != "88.38" || book.Bids[0].Price.String() != "88.36" || book.Bids[1].Price.String() != "88.35" || book.Bids[0].Amount.String() != "120.5" {
		t.Fatalf("unexpected levels or ordering: %+v", book)
	}
	if book.ReceivedAt.Before(before) || book.ReceivedAt.After(after) || book.ReceivedAt.Location() != time.UTC {
		t.Fatalf("unexpected receipt time: %s", book.ReceivedAt)
	}
}

func TestGetOrderBookRejectsInvalidResponses(t *testing.T) {
	tests := []struct {
		name, body, contentType string
		status                  int
	}{
		{name: "server error", body: "unavailable", status: http.StatusServiceUnavailable},
		{name: "not found", body: "not found", status: http.StatusNotFound},
		{name: "malformed JSON", body: `{"ask":`},
		{name: "empty object", body: `{}`},
		{name: "null", body: `null`},
		{name: "empty body", body: ""},
		{name: "HTML", body: "<html>error</html>", contentType: "text/html"},
		{name: "wrong ask symbol", body: strings.Replace(validDepth, "USDT/RUB", "BTC/USDT", 1)},
		{name: "wrong bid symbol", body: strings.Replace(validDepth, `"bid":{"symbol":"USDT/RUB"`, `"bid":{"symbol":"BTC/USDT"`, 1)},
		{name: "empty asks", body: `{"ask":{"symbol":"USDT/RUB","items":[]},"bid":{"symbol":"USDT/RUB","items":[{"price":88,"amount":1}]}}`},
		{name: "empty bids", body: `{"bid":{"symbol":"USDT/RUB","items":[]},"ask":{"symbol":"USDT/RUB","items":[{"price":88,"amount":1}]}}`},
		{name: "invalid decimal", body: strings.Replace(validDepth, "88.370000000000000001", `"oops"`, 1)},
		{name: "zero ask", body: strings.Replace(validDepth, "88.370000000000000001", "0", 1)},
		{name: "negative bid", body: strings.Replace(validDepth, "88.36", "-88.36", 1)},
		{name: "zero ask amount", body: strings.Replace(validDepth, "150.25", "0", 1)},
		{name: "negative bid amount", body: strings.Replace(validDepth, "120.5", "-1", 1)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				contentType := tt.contentType
				if contentType == "" {
					contentType = "application/json"
				}
				w.Header().Set("Content-Type", contentType)
				status := tt.status
				if status == 0 {
					status = http.StatusOK
				}
				w.WriteHeader(status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()
			book, err := NewClient(server.URL, time.Second).GetOrderBook(context.Background(), "USDT/RUB")
			if err == nil {
				t.Fatal("expected an error")
			}
			if len(book.Asks) != 0 || len(book.Bids) != 0 || !book.ReceivedAt.IsZero() {
				t.Fatalf("invalid response returned partial data: %+v", book)
			}
			if tt.status != 0 && !strings.Contains(err.Error(), fmt.Sprint(tt.status)) {
				t.Fatalf("missing HTTP status: %v", err)
			}
		})
	}
}

func TestGetOrderBookRejectsEmptySymbol(t *testing.T) {
	client := NewClient("http://unused.invalid", time.Second)
	for _, symbol := range []string{"", "  "} {
		if _, err := client.GetOrderBook(context.Background(), symbol); err == nil || !strings.Contains(err.Error(), "symbol") {
			t.Fatalf("expected symbol validation error, got %v", err)
		}
	}
}

func TestGetOrderBookCancellation(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := NewClient(server.URL, 2*time.Second).GetOrderBook(ctx, "USDT/RUB")
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("request did not reach server")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected cancellation, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("request was not canceled")
	}
}

func TestGetOrderBookTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	}))
	defer server.Close()
	_, err := NewClient(server.URL, 100*time.Millisecond).GetOrderBook(context.Background(), "USDT/RUB")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected timeout, got %v", err)
	}
}
