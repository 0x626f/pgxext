package functional

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/0x626f/pgxext"
)

const functionalTestSchema = "pgxext_functional_test"

func functionalDataSource(t *testing.T) *pgxext.DataSource {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		if os.Getenv("PGXEXT_REQUIRE_INTEGRATION") == "1" {
			t.Fatal("TEST_DATABASE_URL not set while integration tests are required")
		}
		t.Skip("TEST_DATABASE_URL not set; skipping integration test")
	}
	config := pgxext.NewConfig()
	if _, err := config.WithURL(url); err != nil {
		t.Fatalf("config: %v", err)
	}
	ds, err := pgxext.NewDataSource(context.Background(), config)
	if err != nil {
		t.Fatalf("NewDataSource: %v", err)
	}
	t.Cleanup(ds.Close)
	return ds
}

func TestDatabaseObjectsIntegration(t *testing.T) {
	db := functionalDataSource(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	if _, err := db.Exec(ctx, `DROP SCHEMA IF EXISTS pgxext_functional_test CASCADE`); err != nil {
		t.Fatalf("drop test schema: %v", err)
	}
	t.Cleanup(func() {
		db.Exec(context.Background(), `DROP SCHEMA IF EXISTS pgxext_functional_test CASCADE`) //nolint:errcheck
	})
	for _, statement := range []string{
		`CREATE SCHEMA pgxext_functional_test`,
		`CREATE TABLE pgxext_functional_test.measurements (
            id bigint PRIMARY KEY,
            value integer NOT NULL
        )`,
		`INSERT INTO pgxext_functional_test.measurements (id, value) VALUES (1, 10), (2, 20)`,
	} {
		if _, err := db.Exec(ctx, statement); err != nil {
			t.Fatalf("create fixtures: %v", err)
		}
	}

	t.Run("view apply replace and drop", func(t *testing.T) {
		view := NewView(functionalTestSchema + ".positive_measurements").
			As(`SELECT id, value FROM pgxext_functional_test.measurements WHERE value > 0`).
			WithCheckOption(LocalCheckOption)
		if err := view.Apply(ctx, db); err != nil {
			t.Fatalf("Apply: %v", err)
		}
		assertInt(t, ctx, db, `SELECT count(*) FROM pgxext_functional_test.positive_measurements`, 2)

		replacement := NewView(functionalTestSchema + ".positive_measurements").
			OrReplace().
			As(`SELECT id, value FROM pgxext_functional_test.measurements WHERE value >= 20`).
			WithCheckOption(CascadedCheckOption)
		if err := replacement.Apply(ctx, db); err != nil {
			t.Fatalf("Apply replacement: %v", err)
		}
		assertInt(t, ctx, db, `SELECT count(*) FROM pgxext_functional_test.positive_measurements`, 1)
		if err := replacement.Drop(ctx, db, false); err != nil {
			t.Fatalf("Drop: %v", err)
		}
		if err := replacement.Drop(ctx, db, true); err != nil {
			t.Fatalf("Drop IF EXISTS: %v", err)
		}
	})

	t.Run("materialized view apply refresh and drop", func(t *testing.T) {
		view := NewMaterializedView(functionalTestSchema + ".measurement_totals").
			IfNotExists().
			As(`SELECT id, value FROM pgxext_functional_test.measurements`).
			WithNoData()
		if err := view.Apply(ctx, db); err != nil {
			t.Fatalf("Apply: %v", err)
		}
		if err := view.Apply(ctx, db); err != nil {
			t.Fatalf("idempotent Apply: %v", err)
		}
		if err := view.Refresh(ctx, db, false, true); err != nil {
			t.Fatalf("Refresh WITH DATA: %v", err)
		}
		assertInt(t, ctx, db, `SELECT sum(value) FROM pgxext_functional_test.measurement_totals`, 30)

		if _, err := db.Exec(ctx, `CREATE UNIQUE INDEX measurement_totals_id_idx ON pgxext_functional_test.measurement_totals (id)`); err != nil {
			t.Fatalf("create materialized-view index: %v", err)
		}
		if _, err := db.Exec(ctx, `UPDATE pgxext_functional_test.measurements SET value = 40 WHERE id = 2`); err != nil {
			t.Fatalf("update fixture: %v", err)
		}
		if err := view.Refresh(ctx, db, true, true); err != nil {
			t.Fatalf("concurrent Refresh WITH DATA: %v", err)
		}
		assertInt(t, ctx, db, `SELECT sum(value) FROM pgxext_functional_test.measurement_totals`, 50)

		if err := view.Refresh(ctx, db, false, false); err != nil {
			t.Fatalf("Refresh WITH NO DATA: %v", err)
		}
		if err := view.Refresh(ctx, db, false, true); err != nil {
			t.Fatalf("repopulate after WITH NO DATA: %v", err)
		}
		if err := view.Drop(ctx, db, false); err != nil {
			t.Fatalf("Drop: %v", err)
		}
		if err := view.Drop(ctx, db, true); err != nil {
			t.Fatalf("Drop IF EXISTS: %v", err)
		}

		prepopulated := NewMaterializedView(functionalTestSchema+".prepopulated_measurements").
			As(`SELECT id, value FROM pgxext_functional_test.measurements`).
			WithStorageParameter("fillfactor", "80").
			WithData()
		if err := prepopulated.Apply(ctx, db); err != nil {
			t.Fatalf("Apply WITH DATA: %v", err)
		}
		assertInt(t, ctx, db, `SELECT sum(value) FROM pgxext_functional_test.prepopulated_measurements`, 50)
		if err := prepopulated.Drop(ctx, db, false); err != nil {
			t.Fatalf("Drop prepopulated materialized view: %v", err)
		}
	})

	t.Run("function apply replace and drop", func(t *testing.T) {
		function := NewFunction(functionalTestSchema + ".increment").
			WithArguments("value integer").
			Returns("integer").
			Language("sql").
			WithVolatility(Immutable).
			SecurityDefiner().
			Strict().
			Body(`SELECT value + 1`)
		if err := function.Apply(ctx, db); err != nil {
			t.Fatalf("Apply: %v", err)
		}
		assertInt(t, ctx, db, `SELECT pgxext_functional_test.increment(4)`, 5)

		replacement := NewFunction(functionalTestSchema + ".increment").
			OrReplace().
			WithArguments("value integer").
			Returns("integer").
			Language("sql").
			WithVolatility(Immutable).
			SecurityDefiner().
			Strict().
			Body(`SELECT value + 2`)
		if err := replacement.Apply(ctx, db); err != nil {
			t.Fatalf("Apply replacement: %v", err)
		}
		assertInt(t, ctx, db, `SELECT pgxext_functional_test.increment(4)`, 6)
		if err := replacement.Drop(ctx, db, false); err != nil {
			t.Fatalf("Drop: %v", err)
		}
		if err := replacement.Drop(ctx, db, true); err != nil {
			t.Fatalf("Drop IF EXISTS: %v", err)
		}
	})
}

func assertInt(t *testing.T, ctx context.Context, db *pgxext.DataSource, sql string, want int) {
	t.Helper()
	row, err := db.QueryRow(ctx, sql)
	if err != nil {
		t.Fatalf("QueryRow: %v", err)
	}
	var got int
	if err := row.Scan(&got); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if got != want {
		t.Fatalf("value = %d, want %d", got, want)
	}
}
