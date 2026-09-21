package config

import (
	"flag"
	"fmt"
	"io"
	"os"
	"time"
)

func Load() (*Config, error) {
	return Parse(os.Args[1:], os.Getenv)
}

func Parse(args []string, getenv func(string) string) (*Config, error) {
	env := func(name, fallback string) string {
		if value := getenv(name); value != "" {
			return value
		}
		return fallback
	}
	var cfg Config
	var startup, shutdown string
	flags := flag.NewFlagSet("rapira_rates", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&cfg.DatabaseURL, "database-url", env("DATABASE_URL", "postgres://rates:rates_local@localhost:15433/rapira_rates?sslmode=disable"), "PostgreSQL connection URL")
	flags.StringVar(&cfg.GRPCAddress, "grpc-address", env("GRPC_ADDRESS", ":50052"), "gRPC listen address")
	flags.StringVar(&startup, "startup-timeout", env("STARTUP_TIMEOUT", "5s"), "Database connection timeout")
	flags.StringVar(&shutdown, "shutdown-timeout", env("SHUTDOWN_TIMEOUT", "5s"), "Graceful shutdown timeout")
	if err := flags.Parse(args); err != nil {
		return nil, err
	}
	if flags.NArg() != 0 {
		return nil, fmt.Errorf("unexpected positional arguments")
	}
	var err error
	if cfg.StartupTimeout, err = time.ParseDuration(startup); err != nil {
		return nil, fmt.Errorf("invalid startup timeout: %w", err)
	}
	if cfg.ShutdownTimeout, err = time.ParseDuration(shutdown); err != nil {
		return nil, fmt.Errorf("invalid shutdown timeout: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}
