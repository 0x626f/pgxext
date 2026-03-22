package pgxext

import (
	"context"
	"fmt"
	"testing"
)

// ---------------------------------------------------------------------------
// Test fixtures
// ---------------------------------------------------------------------------

// testMigrations is a deterministic set used across migrator tests.
var testMigrations = MigrationSet{
	{
		Name:      "001_create_users",
		UpQuery:   "CREATE TABLE test_users (id SERIAL PRIMARY KEY, name TEXT NOT NULL)",
		DownQuery: "DROP TABLE test_users",
	},
	{
		Name:      "002_create_posts",
		UpQuery:   "CREATE TABLE test_posts (id SERIAL PRIMARY KEY, title TEXT NOT NULL)",
		DownQuery: "DROP TABLE test_posts",
	},
}

// cleanDB drops every table created by the test fixtures so each test starts
// from a clean slate, regardless of whether a previous test left debris.
func cleanDB(t *testing.T, ds *DataSource) {
	t.Helper()
	ctx := context.Background()
	for _, stmt := range []string{
		"DROP TABLE IF EXISTS test_users",
		"DROP TABLE IF EXISTS test_posts",
		"DROP TABLE IF EXISTS migrations",
	} {
		if _, err := ds.Exec(ctx, stmt); err != nil {
			t.Logf("cleanup %q: %v", stmt, err)
		}
	}
}

// tableExists reports whether a table with the given name exists in the
// current search_path.
func tableExists(t *testing.T, ds *DataSource, table string) bool {
	t.Helper()
	row, err := ds.QueryRow(context.Background(),
		"SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_name = $1)", table)
	if err != nil {
		t.Fatalf("tableExists query: %v", err)
	}
	var exists bool
	if err := row.Scan(&exists); err != nil {
		t.Fatalf("tableExists scan: %v", err)
	}
	return exists
}

// migrationRecorded reports whether a migration name exists in the migrations
// tracking table.
func migrationRecorded(t *testing.T, ds *DataSource, name string) bool {
	t.Helper()
	row, err := ds.QueryRow(context.Background(),
		"SELECT EXISTS(SELECT 1 FROM migrations WHERE name = $1)", name)
	if err != nil {
		t.Fatalf("migrationRecorded query: %v", err)
	}
	var exists bool
	if err := row.Scan(&exists); err != nil {
		t.Fatalf("migrationRecorded scan: %v", err)
	}
	return exists
}

// catchPanic runs fn and returns the recovered value (nil if fn did not panic).
func catchPanic(fn func()) (recovered any) {
	defer func() { recovered = recover() }()
	fn()
	return nil
}

// ---------------------------------------------------------------------------
// MigrationSet unit tests (no DB required)
// ---------------------------------------------------------------------------

func TestMigrationSet_Join(t *testing.T) {
	a := MigrationSet{{Name: "a", UpQuery: "up-a", DownQuery: "down-a"}}
	b := MigrationSet{{Name: "b", UpQuery: "up-b", DownQuery: "down-b"}}

	joined := a.Join(b)
	if len(joined) != 2 {
		t.Fatalf("len = %d, want 2", len(joined))
	}
	if joined[0].Name != "a" || joined[1].Name != "b" {
		t.Errorf("unexpected order: %v", joined)
	}
}

func TestMigrationSet_Join_Empty(t *testing.T) {
	a := MigrationSet{{Name: "a"}}
	joined := a.Join(MigrationSet{})
	if len(joined) != 1 {
		t.Errorf("len = %d, want 1", len(joined))
	}
}

func TestMigrationSet_Join_BothEmpty(t *testing.T) {
	joined := MigrationSet{}.Join(MigrationSet{})
	if len(joined) != 0 {
		t.Errorf("len = %d, want 0", len(joined))
	}
}

// ---------------------------------------------------------------------------
// Migrator integration tests
// ---------------------------------------------------------------------------

func TestMigrator_Up_AppliesMigrations(t *testing.T) {
	ds := integrationDS(t)
	t.Cleanup(func() { cleanDB(t, ds) })
	cleanDB(t, ds)

	m := NewMigrator(context.Background(), ds)
	if err := m.Up(testMigrations); err != nil {
		t.Fatalf("Up: %v", err)
	}

	for _, mig := range testMigrations {
		// Table must exist.
		if !tableExists(t, ds, tableName(mig)) {
			t.Errorf("table for migration %q not created", mig.Name)
		}
		// Record must be in migrations table.
		if !migrationRecorded(t, ds, mig.Name) {
			t.Errorf("migration %q not recorded in migrations table", mig.Name)
		}
	}
}

func TestMigrator_Up_Idempotent(t *testing.T) {
	ds := integrationDS(t)
	t.Cleanup(func() { cleanDB(t, ds) })
	cleanDB(t, ds)

	m := NewMigrator(context.Background(), ds)

	if err := m.Up(testMigrations); err != nil {
		t.Fatalf("first Up: %v", err)
	}
	// Running Up a second time must succeed without error or panic.
	if r := catchPanic(func() { m.Up(testMigrations) }); r != nil { //nolint:errcheck
		t.Fatalf("second Up panicked: %v", r)
	}
}

func TestMigrator_Up_EmptySet(t *testing.T) {
	ds := integrationDS(t)
	t.Cleanup(func() { cleanDB(t, ds) })
	cleanDB(t, ds)

	m := NewMigrator(context.Background(), ds)
	if err := m.Up(MigrationSet{}); err != nil {
		t.Fatalf("Up with empty set: %v", err)
	}
	// The migrations table itself should be created.
	if !tableExists(t, ds, "migrations") {
		t.Error("migrations tracking table not created for empty migration set")
	}
}

func TestMigrator_Down_RevertsInReverseOrder(t *testing.T) {
	ds := integrationDS(t)
	t.Cleanup(func() { cleanDB(t, ds) })
	cleanDB(t, ds)

	m := NewMigrator(context.Background(), ds)
	m.Up(testMigrations) //nolint:errcheck

	if err := m.Down(testMigrations); err != nil {
		t.Fatalf("Down: %v", err)
	}

	for _, mig := range testMigrations {
		if tableExists(t, ds, tableName(mig)) {
			t.Errorf("table for migration %q still exists after Down", mig.Name)
		}
	}
	// The migrations tracking table itself must also be dropped.
	if tableExists(t, ds, "migrations") {
		t.Error("migrations tracking table still exists after Down")
	}
}

func TestMigrator_Up_Down_Roundtrip(t *testing.T) {
	ds := integrationDS(t)
	t.Cleanup(func() { cleanDB(t, ds) })
	cleanDB(t, ds)

	m := NewMigrator(context.Background(), ds)

	m.Up(testMigrations)   //nolint:errcheck
	m.Down(testMigrations) //nolint:errcheck
	// Second Up must succeed after a full Down.
	if r := catchPanic(func() { m.Up(testMigrations) }); r != nil { //nolint:errcheck
		t.Fatalf("Up after Down panicked: %v", r)
	}
	for _, mig := range testMigrations {
		if !tableExists(t, ds, tableName(mig)) {
			t.Errorf("table for migration %q missing after second Up", mig.Name)
		}
	}
}

func TestMigrator_Up_PanicsOnInvalidSQL(t *testing.T) {
	ds := integrationDS(t)
	t.Cleanup(func() { cleanDB(t, ds) })
	cleanDB(t, ds)

	m := NewMigrator(context.Background(), ds)
	bad := MigrationSet{{Name: "bad_migration", UpQuery: "THIS IS NOT VALID SQL"}}

	if r := catchPanic(func() { m.Up(bad) }); r == nil { //nolint:errcheck
		t.Error("expected panic for invalid SQL, got none")
	}
}

func TestMigrator_Down_PanicsOnInvalidSQL(t *testing.T) {
	ds := integrationDS(t)
	t.Cleanup(func() { cleanDB(t, ds) })
	cleanDB(t, ds)

	m := NewMigrator(context.Background(), ds)
	// Apply a valid migration first so Down finds something to revert.
	valid := MigrationSet{{
		Name:      "valid_for_down_test",
		UpQuery:   "CREATE TABLE down_panic_test (id INT)",
		DownQuery: "THIS IS NOT VALID SQL",
	}}
	m.Up(valid) //nolint:errcheck

	if r := catchPanic(func() { m.Down(valid) }); r == nil { //nolint:errcheck
		t.Error("expected panic for invalid DownQuery SQL, got none")
	}
	// cleanup residual
	ds.Exec(context.Background(), "DROP TABLE IF EXISTS down_panic_test") //nolint:errcheck
	ds.Exec(context.Background(), "DROP TABLE IF EXISTS migrations")      //nolint:errcheck
}

func TestMigrator_Down_SkipsNonApplied(t *testing.T) {
	ds := integrationDS(t)
	t.Cleanup(func() { cleanDB(t, ds) })
	cleanDB(t, ds)

	m := NewMigrator(context.Background(), ds)
	// Apply only the first migration.
	m.Up(testMigrations[:1]) //nolint:errcheck

	// Down on the full set: second migration was never applied — must not panic.
	if r := catchPanic(func() { m.Down(testMigrations) }); r != nil { //nolint:errcheck
		t.Fatalf("Down panicked on non-applied migration: %v", r)
	}
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// tableName extracts the user table name from a Migration's DownQuery
// (e.g. "DROP TABLE test_users" → "test_users"). This is intentionally
// fragile — it only works with the test fixtures above.
func tableName(m Migration) string {
	// DownQuery is "DROP TABLE <name>" for all test fixtures.
	var name string
	if _, err := fmt.Sscanf(m.DownQuery, "DROP TABLE %s", &name); err != nil {
		return ""
	}
	return name
}
