package rate

import (
	"errors"
	"testing"

	rateerrors "example.com/rapira_rates/internal/errors"
	"github.com/shopspring/decimal"
)

func TestTopN(t *testing.T) {
	levels := []Level{
		{Price: decimal.RequireFromString("88.370000000000000001"), Amount: decimal.NewFromInt(150)},
		{Price: decimal.RequireFromString("88.38"), Amount: decimal.NewFromInt(1)},
		{Price: decimal.RequireFromString("88.40"), Amount: decimal.NewFromInt(1000)},
	}
	tests := []struct {
		name    string
		levels  []Level
		n       int
		want    string
		wantErr error
	}{
		{name: "first and exact precision", levels: levels, n: 1, want: "88.370000000000000001"},
		{name: "middle", levels: levels, n: 2, want: "88.38"},
		{name: "last", levels: levels, n: 3, want: "88.4"},
		{name: "single level", levels: levels[:1], n: 1, want: "88.370000000000000001"},
		{name: "zero position", levels: levels, n: 0, wantErr: rateerrors.ErrInvalidPosition},
		{name: "negative position", levels: levels, n: -1, wantErr: rateerrors.ErrInvalidPosition},
		{name: "position beyond depth", levels: levels, n: 4, wantErr: rateerrors.ErrInsufficientLevels},
		{name: "empty slice", levels: []Level{}, n: 1, wantErr: rateerrors.ErrInsufficientLevels},
		{name: "nil slice", n: 1, wantErr: rateerrors.ErrInsufficientLevels},
		{name: "invalid position on empty slice", n: 0, wantErr: rateerrors.ErrInvalidPosition},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			price, err := TopN(tt.levels, tt.n)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("expected %v, got %v", tt.wantErr, err)
				}
				if !price.IsZero() {
					t.Fatalf("expected zero on error, got %s", price)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if price.String() != tt.want {
				t.Fatalf("expected price %s, got %s", tt.want, price)
			}
		})
	}
}

func TestAvgNM(t *testing.T) {
	levels := []Level{
		{Price: decimal.RequireFromString("88.37"), Amount: decimal.NewFromInt(1)},
		{Price: decimal.RequireFromString("88.38"), Amount: decimal.NewFromInt(10000)},
		{Price: decimal.RequireFromString("88.40"), Amount: decimal.NewFromInt(150)},
		{Price: decimal.RequireFromString("100"), Amount: decimal.NewFromInt(1)},
	}
	tests := []struct {
		name    string
		levels  []Level
		n, m    int
		want    string
		wantErr error
	}{
		{name: "inclusive subset and unweighted mean", levels: levels, n: 2, m: 3, want: "88.39"},
		{name: "whole book", levels: levels, n: 1, m: 4, want: "91.2875"},
		{name: "one element range", levels: levels, n: 2, m: 2, want: "88.38"},
		{name: "last element", levels: levels, n: 4, m: 4, want: "100"},
		{name: "single level book", levels: levels[:1], n: 1, m: 1, want: "88.37"},
		{name: "repeating fraction", levels: levels, n: 1, m: 3, want: "88.3833333333333333"},
		{name: "round up", levels: []Level{
			{Price: decimal.NewFromInt(1)}, {Price: decimal.NewFromInt(2)}, {Price: decimal.NewFromInt(2)},
		}, n: 1, m: 3, want: "1.6666666666666667"},
		{name: "round half away from zero", levels: []Level{
			{Price: decimal.RequireFromString("1.0000000000000001")}, {Price: decimal.NewFromInt(1)},
		}, n: 1, m: 2, want: "1.0000000000000001"},
		{name: "exact decimal arithmetic", levels: []Level{
			{Price: decimal.RequireFromString("0.1")}, {Price: decimal.RequireFromString("0.2")},
		}, n: 1, m: 2, want: "0.15"},
		{name: "zero start", levels: levels, n: 0, m: 2, wantErr: rateerrors.ErrInvalidPosition},
		{name: "negative start", levels: levels, n: -1, m: 2, wantErr: rateerrors.ErrInvalidPosition},
		{name: "zero end", levels: levels, n: 1, m: 0, wantErr: rateerrors.ErrInvalidPosition},
		{name: "negative end", levels: levels, n: 1, m: -1, wantErr: rateerrors.ErrInvalidPosition},
		{name: "reversed range", levels: levels, n: 3, m: 2, wantErr: rateerrors.ErrInvalidRange},
		{name: "end beyond depth", levels: levels, n: 2, m: 5, wantErr: rateerrors.ErrInsufficientLevels},
		{name: "whole range beyond depth", levels: levels, n: 5, m: 6, wantErr: rateerrors.ErrInsufficientLevels},
		{name: "nil book", n: 1, m: 1, wantErr: rateerrors.ErrInsufficientLevels},
		{name: "empty book", levels: []Level{}, n: 1, m: 1, wantErr: rateerrors.ErrInsufficientLevels},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			price, err := AvgNM(tt.levels, tt.n, tt.m)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("expected %v, got %v", tt.wantErr, err)
				}
				if !price.IsZero() {
					t.Fatalf("expected zero on error, got %s", price)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if price.String() != tt.want {
				t.Fatalf("expected price %s, got %s", tt.want, price)
			}
		})
	}
}
