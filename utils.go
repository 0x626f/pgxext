// Package pgxext provides a thin PostgreSQL connection-pool wrapper built on
// pgx/v5, with helpers for configuration, migrations, and a generic repository
// query builder.
package pgxext

import (
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// CollectOneRow scans the first row of rows into *T using db-tag field mapping.
// Returns nil without an error if no rows are found.
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

// CollectRows scans all rows into []*T using db-tag field mapping.
// Returns nil without an error if no rows are found.
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

// IsPostgresError reports whether err is a PostgreSQL server error and returns
// the underlying *pgconn.PgError for inspection (e.g. Code, Constraint).
func IsPostgresError(err error) (*pgconn.PgError, bool) {
	var dbError *pgconn.PgError
	if errors.As(err, &dbError) {
		return dbError, true
	}
	return nil, false
}
