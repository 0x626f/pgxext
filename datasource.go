package pgxext

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type DataSource struct {
	*pgxpool.Pool
}

func NewDataSource(ctx context.Context, config *Config) (ds *DataSource, err error) {
	if ctx == nil {
		ctx = context.Background()
	}

	ds = &DataSource{}

	if ds.Pool, err = pgxpool.NewWithConfig(ctx, config.Config); err != nil {
		return nil, err
	}

	return
}

func (ds *DataSource) Exec(ctx context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error) {
	return ds.Pool.Exec(ctx, sql, args...)
}

func (ds *DataSource) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return ds.Pool.Query(ctx, sql, args...)
}

func (ds *DataSource) QueryRow(ctx context.Context, sql string, args ...any) (pgx.Row, error) {
	return ds.Pool.QueryRow(ctx, sql, args...), nil
}

func (ds *DataSource) NewTransaction(ctx context.Context) (pgx.Tx, error) {
	return ds.Pool.Begin(ctx)
}

func (ds *DataSource) NewCustomTransaction(ctx context.Context, options pgx.TxOptions) (pgx.Tx, error) {
	return ds.Pool.BeginTx(ctx, options)
}

func (ds *DataSource) NewBatch() *pgx.Batch {
	return &pgx.Batch{}
}

func (ds *DataSource) SendBatch(ctx context.Context, batch *pgx.Batch) (pgx.BatchResults, error) {
	return ds.Pool.SendBatch(ctx, batch), nil
}
