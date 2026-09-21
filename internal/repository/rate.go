package repository

import (
	"context"
	"fmt"

	"example.com/rapira_rates/internal/model/rate"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

type RateRepository struct {
	pool *pgxpool.Pool
}

func NewRateRepository(pool *pgxpool.Pool) *RateRepository {
	return &RateRepository{pool: pool}
}

func (r *RateRepository) Save(ctx context.Context, data rate.Result) (err error) {
	ctx, span := otel.Tracer("example.com/rapira_rates/internal/repository").Start(ctx, "postgres.SaveRate",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("db.system.name", "postgresql"),
			attribute.String("db.collection.name", "rate_results"),
			attribute.String("db.operation.name", "INSERT"),
			attribute.String("rates.symbol", data.Symbol),
			attribute.String("rates.method", string(data.Params.Method)),
		),
	)
	defer func() {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "save rate result failed")
		}
		span.End()
	}()

	query := `
		INSERT INTO rate_results
		(symbol, received_at, ask, bid, method, n, m)
		VALUES ($1, $2, $3::text::numeric, $4::text::numeric, $5, $6, $7)
	`
	var m *int
	if data.Params.Method == rate.MethodAvgNM {
		m = &data.Params.M
	}
	_, err = r.pool.Exec(ctx, query,
		data.Symbol, data.ReceivedAt, data.Ask.String(), data.Bid.String(),
		string(data.Params.Method), data.Params.N, m,
	)
	if err != nil {
		return fmt.Errorf("save rate result: %w", err)
	}
	return nil
}
