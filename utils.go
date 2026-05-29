// Package pgxext provides pgx/v5 helpers.
package pgxext

import (
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// CollectOneRow scans one row into *T.
func CollectOneRow[T any](rows pgx.Rows) (*T, error) {
	result, err := pgx.CollectOneRow(rows, pgx.RowToAddrOfStructByName[T])

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return result, nil
}

// CollectRows scans rows into []*T.
func CollectRows[T any](rows pgx.Rows) ([]*T, error) {
	result, err := pgx.CollectRows(rows, pgx.RowToAddrOfStructByName[T])

	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return result, nil
}

// IsPostgresError unwraps a PostgreSQL server error.
func IsPostgresError(err error) (*pgconn.PgError, bool) {
	var dbError *pgconn.PgError
	if errors.As(err, &dbError) {
		return dbError, true
	}
	return nil, false
}
