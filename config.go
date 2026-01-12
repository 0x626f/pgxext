package pgxext

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	PGXEXTConfigKey = "PGXEXTConfigKey"
)

type Config struct {
	URL                   string        `env:"URL"`
	MaxConnections        int32         `env:"MAX_CONNECTIONS" default:"1"`
	MinIdleConnections    int32         `env:"MIN_IDLE_CONNECTIONS" default:"1"`
	MaxConnectionLifetime time.Duration `env:"MAX_CONNECTION_LIFETIME" default:"0"`
	MaxConnectionIdleTime time.Duration `env:"MAX_CONNECTION_IDLE_TIME" default:"0"`
	HealthCheckDelay      time.Duration `env:"HEALTH_CHECK_DELAY" default:"1m"`
}

func (config *Config) Convert() *pgxpool.Config {
	poolConfig, err := pgxpool.ParseConfig(config.URL)
	if err != nil {
		panic(err)
	}

	poolConfig.MaxConns = config.MaxConnections
	poolConfig.MinConns = config.MinIdleConnections
	poolConfig.MaxConnLifetime = config.MaxConnectionLifetime
	poolConfig.MaxConnIdleTime = config.MaxConnectionIdleTime
	poolConfig.HealthCheckPeriod = config.HealthCheckDelay

	return poolConfig
}

func (config *Config) Context(parent context.Context) context.Context {
	return context.WithValue(parent, PGXEXTConfigKey, config)
}
