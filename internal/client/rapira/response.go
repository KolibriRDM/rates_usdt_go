package rapira

import "github.com/shopspring/decimal"

type levelResponse struct {
	Price  decimal.Decimal `json:"price"`
	Amount decimal.Decimal `json:"amount"`
}

type sideResponse struct {
	Symbol string          `json:"symbol"`
	Items  []levelResponse `json:"items"`
}

type depthResponse struct {
	Ask sideResponse `json:"ask"`
	Bid sideResponse `json:"bid"`
}
