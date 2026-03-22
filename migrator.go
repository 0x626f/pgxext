package pgxext

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

type Migration struct {
	Name      string
	UpQuery   string
	DownQuery string
}

type MigrationSet []Migration

type Migrator struct {
	ds  *DataSource
	ctx context.Context
}

func NewMigrator(ctx context.Context, ds *DataSource) *Migrator {
	return &Migrator{ds: ds, ctx: ctx}
}

func (set MigrationSet) Join(arg MigrationSet) MigrationSet {
	return append(set, arg...)
}

func (migrator *Migrator) Up(migrations MigrationSet) error {
	var err error
	var bundle pgx.Tx

	bundle, err = migrator.ds.NewTransaction(migrator.ctx)
	if err != nil {
		panic(err)
	}

	err = migrator.createMigrationsTable(bundle)
	if err != nil {
		panic(err)
	}

	for _, migration := range migrations {
		var exists bool

		err = bundle.QueryRow(migrator.ctx,
			`SELECT EXISTS(SELECT 1 FROM migrations WHERE name = $1)`,
			migration.Name,
		).Scan(&exists)

		if err != nil {
			_ = bundle.Rollback(migrator.ctx)
			panic(err)
		}

		if exists {
			continue
		}

		_, err = bundle.Exec(migrator.ctx, migration.UpQuery)
		if err != nil {
			_ = bundle.Rollback(migrator.ctx)
			panic(fmt.Sprintf("migration: %v failed: %v", migration.Name, err))
		}

		_, err = bundle.Exec(migrator.ctx,
			`INSERT INTO migrations (name) VALUES ($1)`,
			migration.Name,
		)
		if err != nil {
			_ = bundle.Rollback(migrator.ctx)
			panic(err)
		}
		fmt.Printf("Migration %v processed\n", migration.Name)
	}

	if err = bundle.Commit(migrator.ctx); err != nil {
		panic(err)
	}

	fmt.Printf("All migrations are applied\n")
	return nil
}

func (migrator *Migrator) Down(migrations MigrationSet) error {
	var err error
	var bundle pgx.Tx

	bundle, err = migrator.ds.NewTransaction(migrator.ctx)
	if err != nil {
		panic(err)
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
			panic(err)
		}

		if !exists {
			continue
		}

		_, err = bundle.Exec(migrator.ctx, migration.DownQuery)
		if err != nil {
			_ = bundle.Rollback(migrator.ctx)
			panic(fmt.Sprintf("migration revert: %v failed: %v", migration.Name, err))
		}

		_, err = bundle.Exec(migrator.ctx,
			`DELETE FROM migrations WHERE name = $1`,
			migration.Name,
		)
		if err != nil {
			_ = bundle.Rollback(migrator.ctx)
			panic(err)
		}
		fmt.Printf("Migration %v reverted\n", migration.Name)
	}

	err = migrator.dropMigrationsTable(bundle)
	if err != nil {
		panic(err)
	}

	if err = bundle.Commit(migrator.ctx); err != nil {
		panic(err)
	}

	fmt.Printf("All migrations are reverted\n")
	return nil
}

func (migrator *Migrator) createMigrationsTable(bundle pgx.Tx) (err error) {
	_, err = bundle.Exec(migrator.ctx, `
        CREATE TABLE IF NOT EXISTS migrations (
            name TEXT NOT NULL,
            created_at TIMESTAMP DEFAULT NOW()
        )
    `)
	return
}

func (migrator *Migrator) dropMigrationsTable(bundle pgx.Tx) (err error) {
	_, err = bundle.Exec(migrator.ctx, `DROP TABLE migrations;`)
	return
}
