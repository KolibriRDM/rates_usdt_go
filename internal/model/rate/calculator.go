package rate

import (
	"fmt"

	rateerrors "example.com/rapira_rates/internal/errors"
	"github.com/shopspring/decimal"
)

func TopN(levels []Level, n int) (decimal.Decimal, error) {
	if n < 1 {
		return decimal.Zero, fmt.Errorf("%w: n=%d", rateerrors.ErrInvalidPosition, n)
	}
	if n > len(levels) {
		return decimal.Zero, fmt.Errorf("%w: requested=%d, available=%d", rateerrors.ErrInsufficientLevels, n, len(levels))
	}
	return levels[n-1].Price, nil
}

func AvgNM(levels []Level, n, m int) (decimal.Decimal, error) {
	if n < 1 || m < 1 {
		return decimal.Zero, fmt.Errorf("%w: n=%d, m=%d", rateerrors.ErrInvalidPosition, n, m)
	}
	if m < n {
		return decimal.Zero, fmt.Errorf("%w: n=%d, m=%d", rateerrors.ErrInvalidRange, n, m)
	}
	if m > len(levels) {
		return decimal.Zero, fmt.Errorf("%w: requested=%d, available=%d", rateerrors.ErrInsufficientLevels, m, len(levels))
	}

	sum := decimal.Zero
	for _, level := range levels[n-1 : m] {
		sum = sum.Add(level.Price)
	}
	count := decimal.NewFromInt(int64(m - n + 1))
	return sum.DivRound(count, 16), nil
}
