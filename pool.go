package pgxext

import (
	"context"

	"github.com/0x626f/go-kit/patterns"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var PoolClient = patterns.NewConfigurableSingleton(func(config *Config) (*pgxpool.Pool, error) {
	if config.Context == nil {
		config.Context = context.Background()
	}

	pool, err := pgxpool.NewWithConfig(config.Context, config.Convert())

	if err != nil {
		return nil, err
	}

	return pool, nil
})

func Exec(ctx context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error) {
	return PoolClient.Instance().Exec(ctx, sql, args...)
}

func Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return PoolClient.Instance().Query(ctx, sql, args...)
}

func QueryRow(ctx context.Context, sql string, args ...any) (pgx.Row, error) {
	return PoolClient.Instance().QueryRow(ctx, sql, args...), nil
}

func NewTransaction(ctx context.Context) (pgx.Tx, error) {
	return PoolClient.Instance().Begin(ctx)
}

func NewCustomTransaction(ctx context.Context, options pgx.TxOptions) (pgx.Tx, error) {
	return PoolClient.Instance().BeginTx(ctx, options)
}

func NewBatch() *pgx.Batch {
	return &pgx.Batch{}
}

func SendBatch(ctx context.Context, batch *pgx.Batch) (pgx.BatchResults, error) {
	return PoolClient.Instance().SendBatch(ctx, batch), nil
}
