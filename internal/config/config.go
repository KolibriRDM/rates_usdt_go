package config

import (
	"fmt"
	"net"
	"net/url"
	"time"
)

type Config struct {
	DatabaseURL     string
	GRPCAddress     string
	StartupTimeout  time.Duration
	ShutdownTimeout time.Duration
}

func (c *Config) Validate() error {
	u, err := url.Parse(c.DatabaseURL)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.Hostname() == "" {
		return fmt.Errorf("database-url must be a PostgreSQL URL with a host")
	}
	if _, _, err := net.SplitHostPort(c.GRPCAddress); err != nil {
		return fmt.Errorf("invalid grpc-address: %w", err)
	}
	if c.StartupTimeout <= 0 || c.ShutdownTimeout <= 0 {
		return fmt.Errorf("startup and shutdown timeouts must be positive")
	}
	return nil
}
