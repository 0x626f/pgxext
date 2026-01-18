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

func (set MigrationSet) Join(arg MigrationSet) MigrationSet {
	return append(set, arg...)
}

func UpMigrations(config *Config, migrations MigrationSet) error {
	PoolClient.WithConfig(config).Instance()
	if err := PoolClient.Err(); err != nil {
		return err
	}

	var err error
	var bundle pgx.Tx

	bundle, err = NewTransaction(config.Context)
	if err != nil {
		panic(err)
	}

	err = createMigrationsTable(config.Context, bundle)
	if err != nil {
		panic(err)
	}

	for _, migration := range migrations {
		var exists bool

		err = bundle.QueryRow(config.Context,
			`SELECT EXISTS(SELECT 1 FROM migrations WHERE name = $1)`,
			migration.Name,
		).Scan(&exists)

		if err != nil {
			_ = bundle.Rollback(config.Context)
			panic(err)
		}

		if exists {
			continue
		}

		_, err = bundle.Exec(config.Context, migration.UpQuery)
		if err != nil {
			_ = bundle.Rollback(config.Context)
			panic(fmt.Sprintf("migration: %v failed: %v", migration.Name, err))
		}

		_, err = bundle.Exec(config.Context,
			`INSERT INTO migrations (name) VALUES ($1)`,
			migration.Name,
		)
		if err != nil {
			_ = bundle.Rollback(config.Context)
			panic(err)
		}
		fmt.Printf("Migration %v processed\n", migration.Name)
	}

	if err = bundle.Commit(config.Context); err != nil {
		panic(err)
	}

	fmt.Printf("All migrations are applied\n")
	return nil
}

func DownMigrations(config *Config, migrations MigrationSet) error {
	PoolClient.WithConfig(config).Instance()
	if err := PoolClient.Err(); err != nil {
		return err
	}

	var err error
	var bundle pgx.Tx

	bundle, err = NewTransaction(config.Context)
	if err != nil {
		panic(err)
	}

	for index := len(migrations) - 1; index >= 0; index-- {
		migration := migrations[index]
		var exists bool

		err = bundle.QueryRow(config.Context,
			`SELECT EXISTS(SELECT 1 FROM migrations WHERE name = $1)`,
			migration.Name,
		).Scan(&exists)

		if err != nil {
			_ = bundle.Rollback(config.Context)
			panic(err)
		}

		if !exists {
			continue
		}

		_, err = bundle.Exec(config.Context, migration.DownQuery)
		if err != nil {
			_ = bundle.Rollback(config.Context)
			panic(fmt.Sprintf("migration revert: %v failed: %v", migration.Name, err))
		}

		_, err = bundle.Exec(config.Context,
			`DELETE FROM migrations WHERE name = $1`,
			migration.Name,
		)
		if err != nil {
			_ = bundle.Rollback(config.Context)
			panic(err)
		}
		fmt.Printf("Migration %v reverted\n", migration.Name)
	}

	err = dropMigrationsTable(config.Context, bundle)
	if err != nil {
		panic(err)
	}

	if err = bundle.Commit(config.Context); err != nil {
		panic(err)
	}

	fmt.Printf("All migrations are reverted\n")
	return nil
}

func createMigrationsTable(ctx context.Context, bundle pgx.Tx) (err error) {
	_, err = bundle.Exec(ctx, `
        CREATE TABLE IF NOT EXISTS migrations (
            name TEXT NOT NULL,
            created_at TIMESTAMP DEFAULT NOW()
        )
    `)
	return
}

func dropMigrationsTable(ctx context.Context, bundle pgx.Tx) (err error) {
	_, err = bundle.Exec(ctx, `DROP TABLE migrations;`)
	return
}
