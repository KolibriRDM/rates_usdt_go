package service

import (
	"context"

	app_errors "example.com/rapira_rates/internal/errors"
	"example.com/rapira_rates/internal/model/rate"
	"github.com/shopspring/decimal"
)

type rateRepository interface {
	Save(context.Context, rate.Result) error
}

type orderBookClient interface {
	GetOrderBook(context.Context, string) (rate.OrderBook, error)
}

type RateService struct {
	repo   rateRepository
	client orderBookClient
	symbol string
}

func NewRateService(repo rateRepository, client orderBookClient, symbol string) *RateService {
	return &RateService{
		repo:   repo,
		client: client,
		symbol: symbol,
	}
}

func (s *RateService) GetRates(ctx context.Context, params rate.CalculationParams) (rate.Result, error) {
	if params.N < 1 {
		return rate.Result{}, app_errors.ErrInvalidPosition
	}
	if params.Method != rate.MethodTopN && params.Method != rate.MethodAvgNM {
		return rate.Result{}, app_errors.ErrUnsupportedMethod
	}

	if params.Method == rate.MethodTopN && params.M != 0 {
		return rate.Result{}, app_errors.ErrUnexpectedRangeEnd
	}

	if params.Method == rate.MethodAvgNM && params.M < 1 {
		return rate.Result{}, app_errors.ErrInvalidPosition
	}

	if params.Method == rate.MethodAvgNM && params.M < params.N {
		return rate.Result{}, app_errors.ErrInvalidRange
	}

	book, err := s.client.GetOrderBook(ctx, s.symbol)

	if err != nil {
		return rate.Result{}, err
	}
	var ask, bid decimal.Decimal
	switch params.Method {
	case rate.MethodTopN:
		ask, err = rate.TopN(book.Asks, params.N)
		if err != nil {
			return rate.Result{}, err
		}
		bid, err = rate.TopN(book.Bids, params.N)
		if err != nil {
			return rate.Result{}, err
		}
	case rate.MethodAvgNM:
		ask, err = rate.AvgNM(book.Asks, params.N, params.M)
		if err != nil {
			return rate.Result{}, err
		}
		bid, err = rate.AvgNM(book.Bids, params.N, params.M)
		if err != nil {
			return rate.Result{}, err
		}
	}

	result := rate.Result{
		Symbol:     book.Symbol,
		Ask:        ask,
		Bid:        bid,
		ReceivedAt: book.ReceivedAt,
		Params:     params,
	}
	err = s.repo.Save(ctx, result)
	if err != nil {
		return rate.Result{}, err
	}
	return result, nil

}
