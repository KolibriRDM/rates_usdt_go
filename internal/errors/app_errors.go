package app_errors

import "errors"

var (
	ErrInvalidPosition    = errors.New("position must be greater than zero")
	ErrInvalidRange       = errors.New("range end must not be less than range start")
	ErrInsufficientLevels = errors.New("not enough order book levels")

	ErrUnsupportedMethod  = errors.New("unsupported calculation method")
	ErrUnexpectedRangeEnd = errors.New("m must be zero for topN")
)
