package grpcserver

import (
	"context"
	"errors"
	"log/slog"

	ratev1 "example.com/rapira_rates/gen/rate/v1"
	rateerrors "example.com/rapira_rates/internal/errors"
	"example.com/rapira_rates/internal/model/rate"
	"example.com/rapira_rates/internal/service"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type RateHandler struct {
	ratev1.UnimplementedRateServiceServer
	service *service.RateService
	logger  *slog.Logger
}

func NewRateHandler(service *service.RateService, logger *slog.Logger) *RateHandler {
	return &RateHandler{
		service: service,
		logger:  logger,
	}
}

func (h *RateHandler) GetRates(ctx context.Context, req *ratev1.GetRatesRequest) (*ratev1.GetRatesResponse, error) {
	var method rate.Method
	switch req.GetMethod() {
	case ratev1.CalculationMethod_CALCULATION_METHOD_TOP_N:
		method = rate.MethodTopN
	case ratev1.CalculationMethod_CALCULATION_METHOD_AVG_NM:
		method = rate.MethodAvgNM
	default:
		return nil, status.Error(codes.InvalidArgument, "unsupported calculation method")
	}

	params := rate.CalculationParams{
		Method: method,
		N:      int(req.GetN()),
		M:      int(req.GetM()),
	}
	res, err := h.service.GetRates(ctx, params)
	if err != nil {
		switch {
		case errors.Is(err, rateerrors.ErrInvalidPosition):
			err = status.Error(codes.InvalidArgument, "positions must be greater than zero")
		case errors.Is(err, rateerrors.ErrUnsupportedMethod):
			err = status.Error(codes.InvalidArgument, "unsupported calculation method")
		case errors.Is(err, rateerrors.ErrUnexpectedRangeEnd):
			err = status.Error(codes.InvalidArgument, "m must be zero for topN")
		case errors.Is(err, rateerrors.ErrInvalidRange):
			err = status.Error(codes.InvalidArgument, "m must not be less than n")
		case errors.Is(err, rateerrors.ErrInsufficientLevels):
			err = status.Error(codes.FailedPrecondition, "not enough order book levels")
		case errors.Is(err, context.Canceled):
			err = status.Error(codes.Canceled, "request canceled")
		case errors.Is(err, context.DeadlineExceeded):
			err = status.Error(codes.DeadlineExceeded, "request deadline exceeded")
		default:
			h.logger.Error("Failed to get rates", "error", err)
			err = status.Error(codes.Internal, "failed to get rates")
		}
		return nil, err
	}

	rateData := &ratev1.RateResult{
		Symbol:     res.Symbol,
		Ask:        res.Ask.String(),
		Bid:        res.Bid.String(),
		ReceivedAt: timestamppb.New(res.ReceivedAt),
		Method:     req.GetMethod(),
		N:          uint32(res.Params.N),
		M:          uint32(res.Params.M),
	}
	response := &ratev1.GetRatesResponse{Result: rateData}
	return response, nil
}
