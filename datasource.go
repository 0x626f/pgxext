package pgxext

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DataSource wraps a pgxpool.Pool and exposes the query surface used by the
// rest of the library. A nil context is treated as context.Background().
type DataSource struct {
	*pgxpool.Pool
}

// NewDataSource opens a connection pool using the provided Config.
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

// Exec runs a SQL statement that does not return rows.
func (ds *DataSource) Exec(ctx context.Context, sql string, args ...interface{}) (pgconn.CommandTag, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	return ds.Pool.Exec(ctx, sql, args...)
}

// Query executes a SQL query and returns the resulting rows.
func (ds *DataSource) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	return ds.Pool.Query(ctx, sql, args...)
}

// QueryRow executes a SQL query expected to return at most one row.
func (ds *DataSource) QueryRow(ctx context.Context, sql string, args ...any) (pgx.Row, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	return ds.Pool.QueryRow(ctx, sql, args...), nil
}

// NewTransaction begins a new database transaction with default options.
func (ds *DataSource) NewTransaction(ctx context.Context) (pgx.Tx, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	return ds.Pool.Begin(ctx)
}

// NewCustomTransaction begins a transaction with the specified isolation level and access mode.
func (ds *DataSource) NewCustomTransaction(ctx context.Context, options pgx.TxOptions) (pgx.Tx, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	return ds.Pool.BeginTx(ctx, options)
}

// NewBatch returns an empty pgx.Batch ready to queue statements.
func (ds *DataSource) NewBatch() *pgx.Batch {
	return &pgx.Batch{}
}

// SendBatch dispatches all queued statements in batch as a single round-trip.
func (ds *DataSource) SendBatch(ctx context.Context, batch *pgx.Batch) (pgx.BatchResults, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	return ds.Pool.SendBatch(ctx, batch), nil
}
