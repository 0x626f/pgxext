package timescaledb

import (
	"context"
	"fmt"

	"github.com/0x626f/pgxext"
	"github.com/jackc/pgx/v5"
)

func withTransaction(ctx context.Context, db *pgxext.DataSource, operation string, fn func(pgx.Tx) error) error {
	tx, err := db.NewTransaction(ctx)
	if err != nil {
		return fmt.Errorf("timescaledb: begin %s transaction: %w", operation, err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("timescaledb: commit %s transaction: %w", operation, err)
	}
	return nil
}
