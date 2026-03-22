package pgxext

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
)

// ---------------------------------------------------------------------------
// Shared integration-test helpers
// ---------------------------------------------------------------------------

// integrationDS returns a connected DataSource or skips the test if
// TEST_DATABASE_URL is not set.
func integrationDS(t *testing.T) *DataSource {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping integration test")
	}
	cfg := NewConfig()
	if _, err := cfg.WithURL(url); err != nil {
		t.Fatalf("config: %v", err)
	}
	ds, err := NewDataSource(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewDataSource: %v", err)
	}
	t.Cleanup(func() { ds.Close() })
	return ds
}

// exec runs a SQL statement against ds, failing the test on error.
func exec(t *testing.T, ds *DataSource, sql string, args ...any) {
	t.Helper()
	if _, err := ds.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

// ---------------------------------------------------------------------------
// NewDataSource
// ---------------------------------------------------------------------------

func TestNewDataSource_NilCtx(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	cfg := NewConfig()
	if _, err := cfg.WithURL(url); err != nil {
		t.Fatalf("config: %v", err)
	}
	ds, err := NewDataSource(nil, cfg) // nil ctx must not panic
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer ds.Close()
	if ds.Pool == nil {
		t.Error("Pool is nil")
	}
}

func TestNewDataSource_Valid(t *testing.T) {
	ds := integrationDS(t)
	if err := ds.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

func TestNewDataSource_InvalidHost(t *testing.T) {
	cfg := NewConfig()
	cfg.WithHost("127.0.0.2").
		WithPort(9999).
		WithDatabase("nonexistent").
		WithUser("nobody").
		WithPassword("nopass")
	// pgxpool is lazy — creation succeeds, Ping fails.
	ds, err := NewDataSource(context.Background(), cfg)
	if err != nil {
		return // some drivers do error at creation
	}
	defer ds.Close()
	if err := ds.Ping(context.Background()); err == nil {
		t.Error("expected Ping to fail for unreachable host")
	}
}

// ---------------------------------------------------------------------------
// Exec
// ---------------------------------------------------------------------------

func TestDataSource_Exec(t *testing.T) {
	ds := integrationDS(t)
	t.Cleanup(func() { ds.Exec(context.Background(), "DROP TABLE IF EXISTS ds_exec_test") }) //nolint:errcheck

	exec(t, ds, "CREATE TABLE ds_exec_test (id SERIAL PRIMARY KEY, val TEXT)")
	tag, err := ds.Exec(context.Background(), "INSERT INTO ds_exec_test (val) VALUES ($1)", "hello")
	if err != nil {
		t.Fatalf("Exec INSERT: %v", err)
	}
	if tag.RowsAffected() != 1 {
		t.Errorf("RowsAffected = %d, want 1", tag.RowsAffected())
	}
}

// ---------------------------------------------------------------------------
// Query
// ---------------------------------------------------------------------------

func TestDataSource_Query(t *testing.T) {
	ds := integrationDS(t)
	t.Cleanup(func() { ds.Exec(context.Background(), "DROP TABLE IF EXISTS ds_query_test") }) //nolint:errcheck

	exec(t, ds, "CREATE TABLE ds_query_test (id SERIAL PRIMARY KEY, val TEXT)")
	exec(t, ds, "INSERT INTO ds_query_test (val) VALUES ($1), ($2)", "a", "b")

	rows, err := ds.Query(context.Background(), "SELECT val FROM ds_query_test ORDER BY val")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	defer rows.Close()

	var got []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			t.Fatalf("Scan: %v", err)
		}
		got = append(got, v)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows.Err: %v", err)
	}
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("rows = %v, want [a b]", got)
	}
}

// ---------------------------------------------------------------------------
// QueryRow
// ---------------------------------------------------------------------------

func TestDataSource_QueryRow(t *testing.T) {
	ds := integrationDS(t)
	t.Cleanup(func() { ds.Exec(context.Background(), "DROP TABLE IF EXISTS ds_qrow_test") }) //nolint:errcheck

	exec(t, ds, "CREATE TABLE ds_qrow_test (id SERIAL PRIMARY KEY, val TEXT)")
	exec(t, ds, "INSERT INTO ds_qrow_test (val) VALUES ($1)", "only-row")

	row, err := ds.QueryRow(context.Background(), "SELECT val FROM ds_qrow_test LIMIT 1")
	if err != nil {
		t.Fatalf("QueryRow returned error: %v", err)
	}
	var val string
	if err := row.Scan(&val); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if val != "only-row" {
		t.Errorf("val = %q, want %q", val, "only-row")
	}
}

// ---------------------------------------------------------------------------
// NewTransaction
// ---------------------------------------------------------------------------

func TestDataSource_NewTransaction_Commit(t *testing.T) {
	ds := integrationDS(t)
	t.Cleanup(func() { ds.Exec(context.Background(), "DROP TABLE IF EXISTS ds_tx_test") }) //nolint:errcheck

	exec(t, ds, "CREATE TABLE ds_tx_test (id SERIAL PRIMARY KEY, val TEXT)")

	tx, err := ds.NewTransaction(context.Background())
	if err != nil {
		t.Fatalf("NewTransaction: %v", err)
	}
	if _, err := tx.Exec(context.Background(), "INSERT INTO ds_tx_test (val) VALUES ($1)", "in-tx"); err != nil {
		tx.Rollback(context.Background()) //nolint:errcheck
		t.Fatalf("tx Exec: %v", err)
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	row, _ := ds.QueryRow(context.Background(), "SELECT COUNT(*) FROM ds_tx_test")
	var n int
	row.Scan(&n) //nolint:errcheck
	if n != 1 {
		t.Errorf("row count = %d, want 1", n)
	}
}

func TestDataSource_NewTransaction_Rollback(t *testing.T) {
	ds := integrationDS(t)
	t.Cleanup(func() { ds.Exec(context.Background(), "DROP TABLE IF EXISTS ds_rollback_test") }) //nolint:errcheck

	exec(t, ds, "CREATE TABLE ds_rollback_test (id SERIAL PRIMARY KEY, val TEXT)")

	tx, err := ds.NewTransaction(context.Background())
	if err != nil {
		t.Fatalf("NewTransaction: %v", err)
	}
	tx.Exec(context.Background(), "INSERT INTO ds_rollback_test (val) VALUES ($1)", "will-be-lost") //nolint:errcheck
	if err := tx.Rollback(context.Background()); err != nil {
		t.Fatalf("Rollback: %v", err)
	}

	row, _ := ds.QueryRow(context.Background(), "SELECT COUNT(*) FROM ds_rollback_test")
	var n int
	row.Scan(&n) //nolint:errcheck
	if n != 0 {
		t.Errorf("row count = %d, want 0 after rollback", n)
	}
}

// ---------------------------------------------------------------------------
// NewCustomTransaction
// ---------------------------------------------------------------------------

func TestDataSource_NewCustomTransaction_ReadOnly(t *testing.T) {
	ds := integrationDS(t)

	tx, err := ds.NewCustomTransaction(context.Background(), pgx.TxOptions{
		AccessMode: pgx.ReadOnly,
	})
	if err != nil {
		t.Fatalf("NewCustomTransaction: %v", err)
	}
	defer tx.Rollback(context.Background()) //nolint:errcheck

	// Write inside a read-only transaction must be rejected by Postgres.
	_, err = tx.Exec(context.Background(), "CREATE TEMP TABLE ro_test (id INT)")
	if err == nil {
		t.Error("expected error for DDL inside read-only transaction, got nil")
	}
}

// ---------------------------------------------------------------------------
// Batch
// ---------------------------------------------------------------------------

func TestDataSource_Batch(t *testing.T) {
	ds := integrationDS(t)
	t.Cleanup(func() { ds.Exec(context.Background(), "DROP TABLE IF EXISTS ds_batch_test") }) //nolint:errcheck

	exec(t, ds, "CREATE TABLE ds_batch_test (id SERIAL PRIMARY KEY, val TEXT)")

	batch := ds.NewBatch()
	if batch == nil {
		t.Fatal("NewBatch returned nil")
	}
	batch.Queue("INSERT INTO ds_batch_test (val) VALUES ($1)", "x")
	batch.Queue("INSERT INTO ds_batch_test (val) VALUES ($1)", "y")
	batch.Queue("SELECT COUNT(*) FROM ds_batch_test")

	results, err := ds.SendBatch(context.Background(), batch)
	if err != nil {
		t.Fatalf("SendBatch: %v", err)
	}
	defer results.Close()

	// consume the two INSERTs
	for i := 0; i < 2; i++ {
		if _, err := results.Exec(); err != nil {
			t.Fatalf("batch Exec[%d]: %v", i, err)
		}
	}
	// consume the SELECT
	row := results.QueryRow()
	var n int
	if err := row.Scan(&n); err != nil {
		t.Fatalf("batch QueryRow Scan: %v", err)
	}
	if n != 2 {
		t.Errorf("batch count = %d, want 2", n)
	}
}
