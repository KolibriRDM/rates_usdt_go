package config

import (
	"strings"
	"testing"
	"time"
)

func TestParsePrecedence(t *testing.T) {
	env := map[string]string{
		"DATABASE_URL":     "postgres://env:pass@database/env",
		"GRPC_ADDRESS":     ":6000",
		"STARTUP_TIMEOUT":  "invalid",
		"SHUTDOWN_TIMEOUT": "9s",
	}
	cfg, err := Parse([]string{"--database-url=postgres://flag:pass@localhost/flags", "--startup-timeout=2s"}, func(key string) string { return env[key] })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DatabaseURL != "postgres://flag:pass@localhost/flags" || cfg.GRPCAddress != ":6000" || cfg.StartupTimeout != 2*time.Second || cfg.ShutdownTimeout != 9*time.Second {
		t.Fatalf("unexpected config: %+v", cfg)
	}
}

func TestParseDefaults(t *testing.T) {
	cfg, err := Parse(nil, func(string) string { return "" })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cfg.DatabaseURL, "/rapira_rates?") || cfg.GRPCAddress != ":50052" || cfg.StartupTimeout != 5*time.Second || cfg.ShutdownTimeout != 5*time.Second {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
}

func TestParseRejectsInvalidInput(t *testing.T) {
	for _, args := range [][]string{
		{"--database-url=http://localhost/db"},
		{"--database-url=postgres:///db"},
		{"--grpc-address=localhost"},
		{"--startup-timeout=oops"},
		{"--shutdown-timeout=0s"},
		{"--startup-timeout=-1s"},
		{"--unknown=true"},
		{"unexpected"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			if _, err := Parse(args, func(string) string { return "" }); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}
