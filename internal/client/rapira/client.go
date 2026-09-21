package rapira

import (
	"context"
	"fmt"
	"strings"
	"time"

	"example.com/rapira_rates/internal/model/rate"
	"github.com/go-resty/resty/v2"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type Client struct {
	httpClient *resty.Client
}

func NewClient(baseURL string, timeout time.Duration) *Client {
	client := resty.New().SetBaseURL(baseURL).SetTimeout(timeout)
	return &Client{
		httpClient: client,
	}
}

func (c *Client) GetOrderBook(ctx context.Context, symbol string) (result rate.OrderBook, err error) {
	ctx, span := otel.Tracer("example.com/rapira_rates/internal/client/rapira").Start(ctx, "rapira.GetOrderBook",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attribute.String("rates.symbol", symbol), attribute.String("http.request.method", "POST")),
	)
	defer func() {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "get Rapira order book failed")
		}
		span.End()
	}()

	if strings.TrimSpace(symbol) == "" {
		return rate.OrderBook{}, fmt.Errorf("trading symbol must not be empty")
	}

	var data depthResponse
	response, err := c.httpClient.R().
		SetContext(ctx).
		SetHeader("Accept", "application/json").
		SetMultipartFormData(map[string]string{"symbol": symbol}).
		SetResult(&data).
		Post("/market/exchange-plate-mini")
	if err != nil {
		return rate.OrderBook{}, fmt.Errorf("get Rapira order book: %w", err)
	}
	receivedAt := time.Now().UTC()
	span.SetAttributes(attribute.Int("http.response.status_code", response.StatusCode()))
	if !response.IsSuccess() {
		return rate.OrderBook{}, fmt.Errorf("rapira returned HTTP status %d", response.StatusCode())
	}
	if data.Ask.Symbol != symbol || data.Bid.Symbol != symbol {
		return rate.OrderBook{}, fmt.Errorf("rapira returned an unexpected trading symbol")
	}
	if len(data.Ask.Items) == 0 || len(data.Bid.Items) == 0 {
		return rate.OrderBook{}, fmt.Errorf("rapira returned an empty order book side")
	}

	book := rate.OrderBook{
		Symbol:     symbol,
		ReceivedAt: receivedAt,
		Asks:       make([]rate.Level, 0, len(data.Ask.Items)),
		Bids:       make([]rate.Level, 0, len(data.Bid.Items)),
	}
	for _, item := range data.Ask.Items {
		if !item.Price.IsPositive() || !item.Amount.IsPositive() {
			return rate.OrderBook{}, fmt.Errorf("rapira returned an ask with a non-positive price or amount")
		}
		book.Asks = append(book.Asks, rate.Level{Price: item.Price, Amount: item.Amount})
	}
	for _, item := range data.Bid.Items {
		if !item.Price.IsPositive() || !item.Amount.IsPositive() {
			return rate.OrderBook{}, fmt.Errorf("rapira returned a bid with a non-positive price or amount")
		}
		book.Bids = append(book.Bids, rate.Level{Price: item.Price, Amount: item.Amount})
	}
	return book, nil
}
