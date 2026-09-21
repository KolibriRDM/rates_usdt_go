package rate

import (
	"time"

	"github.com/shopspring/decimal"
)

type Level struct {
	Price  decimal.Decimal
	Amount decimal.Decimal
}

type OrderBook struct {
	Symbol     string
	Asks       []Level
	Bids       []Level
	ReceivedAt time.Time
}

type Method string

const (
	MethodTopN  Method = "topN"
	MethodAvgNM Method = "avgNM"
)

type CalculationParams struct {
	Method Method
	N      int
	M      int
}

type Result struct {
	Symbol     string
	Ask        decimal.Decimal
	Bid        decimal.Decimal
	ReceivedAt time.Time
	Params     CalculationParams
}
