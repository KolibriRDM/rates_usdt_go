package storage

import (
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

func TestNewPostgresInvalidURL(t *testing.T) {
	pool, err := NewPostgres(context.Background(), "postgres://%zz")
	if pool != nil {
		pool.Close()
		t.Fatal("expected no pool for an invalid URL")
	}
	if err == nil || !strings.Contains(err.Error(), "create database pool:") {
		t.Fatalf("expected a wrapped configuration error, got %v", err)
	}
}

func TestNewPostgresCanceledContext(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	expired, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer stop()
	for _, tt := range []struct {
		name string
		ctx  context.Context
		want error
	}{
		{name: "canceled", ctx: canceled, want: context.Canceled},
		{name: "expired", ctx: expired, want: context.DeadlineExceeded},
	} {
		t.Run(tt.name, func(t *testing.T) {
			pool, err := NewPostgres(tt.ctx, "postgres://test:test@127.0.0.1:1/test?sslmode=disable")
			if pool != nil {
				pool.Close()
				t.Fatal("expected no pool on failure")
			}
			if !errors.Is(err, tt.want) {
				t.Fatalf("expected %v, got %v", tt.want, err)
			}
		})
	}
}

func TestNewPostgresConnectionFailure(t *testing.T) {
	for _, stall := range []bool{false, true} {
		name := "server closes connection"
		if stall {
			name = "startup timeout"
		}
		t.Run(name, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			finished := make(chan struct{})
			release := make(chan struct{})
			go func() {
				defer close(finished)
				conn, acceptErr := listener.Accept()
				if acceptErr != nil {
					return
				}
				if stall {
					<-release
				}
				_ = conn.Close()
			}()
			t.Cleanup(func() {
				close(release)
				_ = listener.Close()
				<-finished
			})
			timeout := 2 * time.Second
			if stall {
				timeout = 200 * time.Millisecond
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()
			pool, err := NewPostgres(ctx, "postgres://test:test@"+listener.Addr().String()+"/test?sslmode=disable")
			if pool != nil {
				pool.Close()
				t.Fatal("expected no pool on connection failure")
			}
			if err == nil || !strings.Contains(err.Error(), "connect to database:") {
				t.Fatalf("expected a wrapped connection error, got %v", err)
			}
			if stall && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("expected deadline exceeded, got %v", err)
			}
		})
	}
}

func TestNewPostgresIntegration(t *testing.T) {
	dsn := os.Getenv("RAPIRA_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set RAPIRA_TEST_DATABASE_URL to run PostgreSQL integration tests")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pool, err := NewPostgres(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("returned pool is unusable: %v", err)
	}
	pool.Close()
	if err := pool.Ping(ctx); err == nil {
		t.Fatal("closed pool must reject new operations")
	}
}
