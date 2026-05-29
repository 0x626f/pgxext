package pgxext

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DataSource wraps a pgxpool.Pool.
type DataSource struct {
	*pgxpool.Pool
}

// NewDataSource opens a connection pool.
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

// Exec runs a SQL statement.
func (ds *DataSource) Exec(ctx context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	return ds.Pool.Exec(ctx, sql, args...)
}

// Query runs a SQL query.
func (ds *DataSource) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	return ds.Pool.Query(ctx, sql, args...)
}

// QueryRow runs a SQL query and returns one row.
func (ds *DataSource) QueryRow(ctx context.Context, sql string, args ...any) (pgx.Row, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	return ds.Pool.QueryRow(ctx, sql, args...), nil
}

// NewTransaction starts a transaction.
func (ds *DataSource) NewTransaction(ctx context.Context) (pgx.Tx, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	return ds.Pool.Begin(ctx)
}

// NewCustomTransaction starts a transaction with options.
func (ds *DataSource) NewCustomTransaction(ctx context.Context, options pgx.TxOptions) (pgx.Tx, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	return ds.Pool.BeginTx(ctx, options)
}

// NewBatch returns an empty batch.
func (ds *DataSource) NewBatch() *pgx.Batch {
	return &pgx.Batch{}
}

// SendBatch sends a batch.
func (ds *DataSource) SendBatch(ctx context.Context, batch *pgx.Batch) (pgx.BatchResults, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	return ds.Pool.SendBatch(ctx, batch), nil
}
