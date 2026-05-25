// Package migration provides a simple transactional SQL migration runner
// backed by a pgxext.DataSource. Applied migrations are recorded in a
// "migrations" table so each script runs exactly once.
package migration

import (
	"context"
	"fmt"

	"github.com/0x626f/pgxext"
	"github.com/jackc/pgx/v5"
)

// Migration holds the name and SQL scripts for a single schema change.
type Migration struct {
	Name      string
	UpQuery   string
	DownQuery string
}

// MigrationSet is an ordered slice of migrations.
type MigrationSet []Migration

// Migrator applies and reverts migrations against a DataSource.
type Migrator struct {
	ds  *pgxext.DataSource
	ctx context.Context
}

// NewMigrator creates a Migrator that runs all operations under ctx.
func NewMigrator(ctx context.Context, ds *pgxext.DataSource) *Migrator {
	return &Migrator{ds: ds, ctx: ctx}
}

// Join concatenates two MigrationSets into one.
func (set MigrationSet) Join(arg MigrationSet) MigrationSet {
	return append(set, arg...)
}

// Up applies all migrations that have not yet been recorded in the migrations
// table. The entire batch runs inside a single transaction.
func (migrator *Migrator) Up(migrations MigrationSet) error {
	var err error
	var bundle pgx.Tx

	bundle, err = migrator.ds.NewTransaction(migrator.ctx)
	if err != nil {
		return err
	}

	err = migrator.createMigrationsTable(bundle)
	if err != nil {
		_ = bundle.Rollback(migrator.ctx)
		return err
	}

	for _, migration := range migrations {
		var exists bool

		err = bundle.QueryRow(migrator.ctx,
			`SELECT EXISTS(SELECT 1 FROM migrations WHERE name = $1)`,
			migration.Name,
		).Scan(&exists)

		if err != nil {
			_ = bundle.Rollback(migrator.ctx)
			return err
		}

		if exists {
			continue
		}

		_, err = bundle.Exec(migrator.ctx, migration.UpQuery)
		if err != nil {
			_ = bundle.Rollback(migrator.ctx)
			return fmt.Errorf("migration: %v failed: %w", migration.Name, err)
		}

		_, err = bundle.Exec(migrator.ctx,
			`INSERT INTO migrations (name) VALUES ($1)`,
			migration.Name,
		)
		if err != nil {
			_ = bundle.Rollback(migrator.ctx)
			return err
		}
		fmt.Printf("Migration %v processed\n", migration.Name)
	}

	if err = bundle.Commit(migrator.ctx); err != nil {
		return err
	}

	fmt.Printf("All migrations are applied\n")
	return nil
}

// Down reverts all migrations in reverse order, removing their records from
// the migrations table. The entire batch runs inside a single transaction.
func (migrator *Migrator) Down(migrations MigrationSet) error {
	var err error
	var bundle pgx.Tx

	bundle, err = migrator.ds.NewTransaction(migrator.ctx)
	if err != nil {
		return err
	}

	err = migrator.createMigrationsTable(bundle)
	if err != nil {
		_ = bundle.Rollback(migrator.ctx)
		return err
	}

	for index := len(migrations) - 1; index >= 0; index-- {
		migration := migrations[index]
		var exists bool

		err = bundle.QueryRow(migrator.ctx,
			`SELECT EXISTS(SELECT 1 FROM migrations WHERE name = $1)`,
			migration.Name,
		).Scan(&exists)

		if err != nil {
			_ = bundle.Rollback(migrator.ctx)
			return err
		}

		if !exists {
			continue
		}

		_, err = bundle.Exec(migrator.ctx, migration.DownQuery)
		if err != nil {
			_ = bundle.Rollback(migrator.ctx)
			return fmt.Errorf("migration revert: %v failed: %w", migration.Name, err)
		}

		_, err = bundle.Exec(migrator.ctx,
			`DELETE FROM migrations WHERE name = $1`,
			migration.Name,
		)
		if err != nil {
			_ = bundle.Rollback(migrator.ctx)
			return err
		}
		fmt.Printf("Migration %v reverted\n", migration.Name)
	}

	empty, err := migrator.migrationsTableEmpty(bundle)
	if err != nil {
		_ = bundle.Rollback(migrator.ctx)
		return err
	}
	if empty {
		err = migrator.dropMigrationsTable(bundle)
		if err != nil {
			_ = bundle.Rollback(migrator.ctx)
			return err
		}
	}

	if err = bundle.Commit(migrator.ctx); err != nil {
		return err
	}

	fmt.Printf("All migrations are reverted\n")
	return nil
}

func (migrator *Migrator) createMigrationsTable(bundle pgx.Tx) (err error) {
	_, err = bundle.Exec(migrator.ctx, `
        CREATE TABLE IF NOT EXISTS migrations (
            name TEXT PRIMARY KEY,
            created_at TIMESTAMP DEFAULT NOW()
        )
    `)
	return
}

func (migrator *Migrator) migrationsTableEmpty(bundle pgx.Tx) (empty bool, err error) {
	err = bundle.QueryRow(migrator.ctx, `SELECT NOT EXISTS(SELECT 1 FROM migrations)`).Scan(&empty)
	return
}

func (migrator *Migrator) dropMigrationsTable(bundle pgx.Tx) (err error) {
	_, err = bundle.Exec(migrator.ctx, `DROP TABLE migrations;`)
	return
}
