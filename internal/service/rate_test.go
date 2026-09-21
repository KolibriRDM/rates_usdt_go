package service_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	app_errors "example.com/rapira_rates/internal/errors"
	"example.com/rapira_rates/internal/model/rate"
	"example.com/rapira_rates/internal/service"
	"github.com/shopspring/decimal"
)

type fakeRateRepository struct {
	save func(context.Context, rate.Result) error
}

func (r fakeRateRepository) Save(ctx context.Context, result rate.Result) error {
	return r.save(ctx, result)
}

type fakeOrderBookClient struct {
	getOrderBook func(context.Context, string) (rate.OrderBook, error)
}

func (c fakeOrderBookClient) GetOrderBook(ctx context.Context, symbol string) (rate.OrderBook, error) {
	return c.getOrderBook(ctx, symbol)
}

func orderBook() rate.OrderBook {
	return rate.OrderBook{
		Symbol:     "USDT/RUB",
		ReceivedAt: time.Date(2026, time.September, 21, 12, 30, 0, 123456789, time.UTC),
		Asks: []rate.Level{
			{Price: decimal.RequireFromString("88.370000000000000001"), Amount: decimal.NewFromInt(1)},
			{Price: decimal.RequireFromString("88.38"), Amount: decimal.NewFromInt(100)},
			{Price: decimal.RequireFromString("88.40"), Amount: decimal.NewFromInt(10000)},
		},
		Bids: []rate.Level{
			{Price: decimal.RequireFromString("88.360000000000000009"), Amount: decimal.NewFromInt(10000)},
			{Price: decimal.RequireFromString("88.35"), Amount: decimal.NewFromInt(100)},
			{Price: decimal.RequireFromString("88.33"), Amount: decimal.NewFromInt(1)},
		},
	}
}

func assertResult(t *testing.T, got, want rate.Result) {
	t.Helper()
	if got.Symbol != want.Symbol || !got.Ask.Equal(want.Ask) || !got.Bid.Equal(want.Bid) || !got.ReceivedAt.Equal(want.ReceivedAt) || got.Params != want.Params {
		t.Fatalf("result=%+v, want %+v", got, want)
	}
}

func assertErrorResult(t *testing.T, result rate.Result, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("error=%v, want %v", err, want)
	}
	assertResult(t, result, rate.Result{})
}

func TestGetRatesSuccess(t *testing.T) {
	for _, tt := range []struct {
		name     string
		params   rate.CalculationParams
		ask, bid string
	}{
		{name: "topN exact decimals", params: rate.CalculationParams{Method: rate.MethodTopN, N: 1}, ask: "88.370000000000000001", bid: "88.360000000000000009"},
		{name: "topN last level", params: rate.CalculationParams{Method: rate.MethodTopN, N: 3}, ask: "88.40", bid: "88.33"},
		{name: "avgNM inclusive unweighted range", params: rate.CalculationParams{Method: rate.MethodAvgNM, N: 2, M: 3}, ask: "88.39", bid: "88.34"},
		{name: "avgNM single level", params: rate.CalculationParams{Method: rate.MethodAvgNM, N: 2, M: 2}, ask: "88.38", bid: "88.35"},
		{name: "avgNM rounding", params: rate.CalculationParams{Method: rate.MethodAvgNM, N: 1, M: 3}, ask: "88.3833333333333333", bid: "88.3466666666666667"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			type requestKey struct{}
			ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), requestKey{}, t.Name()), time.Minute)
			defer cancel()
			assertContext := func(got context.Context) {
				t.Helper()
				deadline, ok := got.Deadline()
				wantDeadline, _ := ctx.Deadline()
				if got.Value(requestKey{}) != t.Name() || !ok || !deadline.Equal(wantDeadline) {
					t.Fatal("dependency did not receive the request context values and deadline")
				}
			}
			book := orderBook()
			want := rate.Result{
				Symbol: book.Symbol, ReceivedAt: book.ReceivedAt, Params: tt.params,
				Ask: decimal.RequireFromString(tt.ask), Bid: decimal.RequireFromString(tt.bid),
			}
			var calls []string
			client := fakeOrderBookClient{getOrderBook: func(gotCtx context.Context, symbol string) (rate.OrderBook, error) {
				calls = append(calls, "fetch")
				assertContext(gotCtx)
				if symbol != "USDT/RUB" {
					t.Fatalf("requested symbol=%q, want USDT/RUB", symbol)
				}
				return book, nil
			}}
			repo := fakeRateRepository{save: func(gotCtx context.Context, result rate.Result) error {
				calls = append(calls, "save")
				assertContext(gotCtx)
				assertResult(t, result, want)
				return nil
			}}
			result, err := service.NewRateService(repo, client, "USDT/RUB").GetRates(ctx, tt.params)
			if err != nil {
				t.Fatal(err)
			}
			assertResult(t, result, want)
			if !slices.Equal(calls, []string{"fetch", "save"}) {
				t.Fatalf("unexpected dependency calls: %v", calls)
			}
		})
	}
}

func TestGetRatesInvalidParametersDoNotCallDependencies(t *testing.T) {
	for _, tt := range []struct {
		name   string
		params rate.CalculationParams
		want   error
	}{
		{name: "zero n", params: rate.CalculationParams{Method: rate.MethodTopN}, want: app_errors.ErrInvalidPosition},
		{name: "negative n", params: rate.CalculationParams{Method: rate.MethodTopN, N: -1}, want: app_errors.ErrInvalidPosition},
		{name: "empty method", params: rate.CalculationParams{N: 1}, want: app_errors.ErrUnsupportedMethod},
		{name: "unknown method", params: rate.CalculationParams{Method: "other", N: 1}, want: app_errors.ErrUnsupportedMethod},
		{name: "topN positive m", params: rate.CalculationParams{Method: rate.MethodTopN, N: 1, M: 2}, want: app_errors.ErrUnexpectedRangeEnd},
		{name: "topN negative m", params: rate.CalculationParams{Method: rate.MethodTopN, N: 1, M: -1}, want: app_errors.ErrUnexpectedRangeEnd},
		{name: "avgNM zero n", params: rate.CalculationParams{Method: rate.MethodAvgNM, M: 1}, want: app_errors.ErrInvalidPosition},
		{name: "avgNM zero m", params: rate.CalculationParams{Method: rate.MethodAvgNM, N: 1}, want: app_errors.ErrInvalidPosition},
		{name: "avgNM negative m", params: rate.CalculationParams{Method: rate.MethodAvgNM, N: 1, M: -1}, want: app_errors.ErrInvalidPosition},
		{name: "avgNM reversed range", params: rate.CalculationParams{Method: rate.MethodAvgNM, N: 3, M: 2}, want: app_errors.ErrInvalidRange},
	} {
		t.Run(tt.name, func(t *testing.T) {
			client := fakeOrderBookClient{getOrderBook: func(context.Context, string) (rate.OrderBook, error) {
				t.Fatal("invalid input reached the order book client")
				return rate.OrderBook{}, nil
			}}
			repo := fakeRateRepository{save: func(context.Context, rate.Result) error {
				t.Fatal("invalid input reached the repository")
				return nil
			}}
			result, err := service.NewRateService(repo, client, "USDT/RUB").GetRates(context.Background(), tt.params)
			assertErrorResult(t, result, err, tt.want)
		})
	}
}

func TestGetRatesInsufficientLevelsDoNotSave(t *testing.T) {
	for _, method := range []rate.Method{rate.MethodTopN, rate.MethodAvgNM} {
		for _, side := range []string{"asks", "bids"} {
			for _, size := range []int{0, 2} {
				t.Run(fmt.Sprintf("%s/%s/levels=%d", method, side, size), func(t *testing.T) {
					book := orderBook()
					if side == "asks" {
						book.Asks = book.Asks[:size]
					} else {
						book.Bids = book.Bids[:size]
					}
					calls := 0
					client := fakeOrderBookClient{getOrderBook: func(context.Context, string) (rate.OrderBook, error) {
						calls++
						return book, nil
					}}
					repo := fakeRateRepository{save: func(context.Context, rate.Result) error {
						t.Fatal("incomplete calculation reached the repository")
						return nil
					}}
					params := rate.CalculationParams{Method: method, N: 3}
					if method == rate.MethodAvgNM {
						params.N, params.M = 1, 3
					}
					result, err := service.NewRateService(repo, client, "USDT/RUB").GetRates(context.Background(), params)
					assertErrorResult(t, result, err, app_errors.ErrInsufficientLevels)
					if calls != 1 {
						t.Fatalf("client calls=%d, want 1", calls)
					}
				})
			}
		}
	}
}

func TestGetRatesDependencyErrors(t *testing.T) {
	for _, source := range []string{"client", "repository"} {
		for _, cause := range []error{errors.New("dependency unavailable"), context.Canceled, context.DeadlineExceeded} {
			t.Run(source+"/"+cause.Error(), func(t *testing.T) {
				failure := fmt.Errorf("dependency failure: %w", cause)
				var calls []string
				client := fakeOrderBookClient{getOrderBook: func(context.Context, string) (rate.OrderBook, error) {
					calls = append(calls, "fetch")
					if source == "client" {
						return orderBook(), failure
					}
					return orderBook(), nil
				}}
				repo := fakeRateRepository{save: func(context.Context, rate.Result) error {
					calls = append(calls, "save")
					return failure
				}}
				result, err := service.NewRateService(repo, client, "USDT/RUB").GetRates(context.Background(), rate.CalculationParams{Method: rate.MethodTopN, N: 1})
				assertErrorResult(t, result, err, cause)
				wantCalls := []string{"fetch"}
				if source == "repository" {
					wantCalls = append(wantCalls, "save")
				}
				if !slices.Equal(calls, wantCalls) {
					t.Fatalf("calls=%v, want %v", calls, wantCalls)
				}
			})
		}
	}
}

func TestGetRatesFetchesAndSavesEveryCall(t *testing.T) {
	book := orderBook()
	params := rate.CalculationParams{Method: rate.MethodTopN, N: 1}
	var calls []string
	fetchCount := 0
	client := fakeOrderBookClient{getOrderBook: func(context.Context, string) (rate.OrderBook, error) {
		calls = append(calls, "fetch")
		fetchCount++
		current := book
		current.ReceivedAt = book.ReceivedAt.Add(time.Duration(fetchCount) * time.Second)
		current.Asks = []rate.Level{{Price: decimal.NewFromInt(int64(90 + fetchCount))}}
		current.Bids = []rate.Level{{Price: decimal.NewFromInt(int64(89 + fetchCount))}}
		return current, nil
	}}
	var saved []rate.Result
	repo := fakeRateRepository{save: func(_ context.Context, result rate.Result) error {
		calls = append(calls, "save")
		saved = append(saved, result)
		return nil
	}}
	svc := service.NewRateService(repo, client, "USDT/RUB")
	for i := 1; i <= 2; i++ {
		result, err := svc.GetRates(context.Background(), params)
		if err != nil {
			t.Fatal(err)
		}
		want := rate.Result{
			Symbol: book.Symbol, Params: params,
			ReceivedAt: book.ReceivedAt.Add(time.Duration(i) * time.Second),
			Ask:        decimal.NewFromInt(int64(90 + i)), Bid: decimal.NewFromInt(int64(89 + i)),
		}
		assertResult(t, result, want)
		if len(saved) != i {
			t.Fatalf("save calls=%d, want %d", len(saved), i)
		}
		assertResult(t, saved[i-1], want)
	}
	if !slices.Equal(calls, []string{"fetch", "save", "fetch", "save"}) {
		t.Fatalf("unexpected dependency calls: %v", calls)
	}
}
