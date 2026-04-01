package repository

import (
	"context"
	"os"
	"testing"

	"github.com/0x626f/pgxext"
)

// ---------------------------------------------------------------------------
// Test schema
// ---------------------------------------------------------------------------

// testItem maps to the test_repo_items table created by setupDB.
type testItem struct {
	ID       int    `db:"id"`
	Name     string `db:"name"`
	Score    int    `db:"score"`
	Category string `db:"category"`
}

// testTag maps to the test_repo_tags table used in JOIN tests.
type testTag struct {
	ID    int    `db:"id"`
	Label string `db:"label"`
}

const createItems = `
CREATE TABLE IF NOT EXISTS test_repo_items (
	id       SERIAL PRIMARY KEY,
	name     TEXT NOT NULL,
	score    INT  NOT NULL DEFAULT 0,
	category TEXT NOT NULL DEFAULT ''
)`

const createTags = `
CREATE TABLE IF NOT EXISTS test_repo_tags (
	id    SERIAL PRIMARY KEY,
	label TEXT NOT NULL
)`

// ---------------------------------------------------------------------------
// Shared helpers
// ---------------------------------------------------------------------------

// integrationDS returns a connected *pgxext.DataSource or skips the test if
// TEST_DATABASE_URL is not set.
func integrationDS(t *testing.T) *pgxext.DataSource {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set; skipping integration test")
	}
	cfg := pgxext.NewConfig()
	if _, err := cfg.WithURL(url); err != nil {
		t.Fatalf("config: %v", err)
	}
	ds, err := pgxext.NewDataSource(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewDataSource: %v", err)
	}
	t.Cleanup(ds.Close)
	return ds
}

// setupDB creates the test tables and registers a cleanup that drops them.
func setupDB(t *testing.T, ds *pgxext.DataSource) {
	t.Helper()
	ctx := context.Background()
	for _, stmt := range []string{createItems, createTags} {
		if _, err := ds.Exec(ctx, stmt); err != nil {
			t.Fatalf("setupDB: %v", err)
		}
	}
	t.Cleanup(func() { cleanupDB(t, ds) })
	cleanupDB(t, ds) // start clean
}

// cleanupDB truncates the test tables so each test starts from a clean slate.
func cleanupDB(t *testing.T, ds *pgxext.DataSource) {
	t.Helper()
	ctx := context.Background()
	for _, stmt := range []string{
		"TRUNCATE test_repo_items RESTART IDENTITY CASCADE",
		"TRUNCATE test_repo_tags  RESTART IDENTITY CASCADE",
	} {
		if _, err := ds.Exec(ctx, stmt); err != nil {
			// Tables might not exist yet on the very first call.
			t.Logf("cleanupDB %q: %v", stmt, err)
		}
	}
}

// insertItem is a convenience wrapper that inserts a row and returns the
// auto-assigned id.
func insertItem(t *testing.T, ds *pgxext.DataSource, name string, score int, category string) int {
	t.Helper()
	row, err := ds.QueryRow(context.Background(),
		"INSERT INTO test_repo_items (name, score, category) VALUES ($1,$2,$3) RETURNING id",
		name, score, category)
	if err != nil {
		t.Fatalf("insertItem query: %v", err)
	}
	var id int
	if err := row.Scan(&id); err != nil {
		t.Fatalf("insertItem scan: %v", err)
	}
	return id
}

// insertTag is a convenience wrapper that inserts a tag row.
func insertTag(t *testing.T, ds *pgxext.DataSource, label string) int {
	t.Helper()
	row, err := ds.QueryRow(context.Background(),
		"INSERT INTO test_repo_tags (label) VALUES ($1) RETURNING id", label)
	if err != nil {
		t.Fatalf("insertTag query: %v", err)
	}
	var id int
	if err := row.Scan(&id); err != nil {
		t.Fatalf("insertTag scan: %v", err)
	}
	return id
}

// ---------------------------------------------------------------------------
// Select tests
// ---------------------------------------------------------------------------

func TestSelect_AllRows(t *testing.T) {
	ds := integrationDS(t)
	setupDB(t, ds)

	insertItem(t, ds, "alpha", 10, "a")
	insertItem(t, ds, "beta", 20, "b")

	repo := NewRepository[testItem](ds, "test_repo_items")
	rows, err := repo.Select().Execute(context.Background())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
}

func TestSelect_EmptyTable(t *testing.T) {
	ds := integrationDS(t)
	setupDB(t, ds)

	repo := NewRepository[testItem](ds, "test_repo_items")
	rows, err := repo.Select().Execute(context.Background())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("got %d rows, want 0", len(rows))
	}
}

func TestSelect_WhereEq(t *testing.T) {
	ds := integrationDS(t)
	setupDB(t, ds)

	insertItem(t, ds, "alpha", 10, "x")
	insertItem(t, ds, "beta", 20, "y")

	repo := NewRepository[testItem](ds, "test_repo_items")
	rows, err := repo.Select().Where("name", Equals, "alpha").Execute(context.Background())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(rows) != 1 || rows[0].Name != "alpha" {
		t.Fatalf("got %v, want [{Name:alpha}]", rows)
	}
}

func TestSelect_MultipleWheres(t *testing.T) {
	ds := integrationDS(t)
	setupDB(t, ds)

	insertItem(t, ds, "alpha", 10, "x")
	insertItem(t, ds, "alpha", 20, "y")
	insertItem(t, ds, "beta", 10, "x")

	repo := NewRepository[testItem](ds, "test_repo_items")
	rows, err := repo.Select().
		Where("name", Equals, "alpha").
		Where("score", Equals, 10).
		Execute(context.Background())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
}

func TestSelect_OrWhere(t *testing.T) {
	ds := integrationDS(t)
	setupDB(t, ds)

	insertItem(t, ds, "alpha", 10, "x")
	insertItem(t, ds, "beta", 20, "y")
	insertItem(t, ds, "gamma", 30, "z")

	repo := NewRepository[testItem](ds, "test_repo_items")
	// WHERE name = 'alpha' OR name = 'gamma'
	rows, err := repo.Select().
		Where("name", Equals, "alpha").
		OrWhere("name", Equals, "gamma").
		Execute(context.Background())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
}

func TestSelect_OrWhereWithAnd(t *testing.T) {
	ds := integrationDS(t)
	setupDB(t, ds)

	insertItem(t, ds, "alpha", 10, "x")
	insertItem(t, ds, "beta", 20, "y")
	insertItem(t, ds, "gamma", 30, "x")

	repo := NewRepository[testItem](ds, "test_repo_items")
	// WHERE (name = 'alpha' OR name = 'gamma') AND category = 'x'
	rows, err := repo.Select().
		Where("name", Equals, "alpha").
		OrWhere("name", Equals, "gamma").
		Where("category", Equals, "x").
		Execute(context.Background())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	for _, r := range rows {
		if r.Category != "x" {
			t.Fatalf("unexpected category %q, want x", r.Category)
		}
	}
}

func TestSelect_WhereIn(t *testing.T) {
	ds := integrationDS(t)
	setupDB(t, ds)

	insertItem(t, ds, "alpha", 10, "x")
	insertItem(t, ds, "beta", 20, "y")
	insertItem(t, ds, "gamma", 30, "z")

	repo := NewRepository[testItem](ds, "test_repo_items")
	rows, err := repo.Select().
		Where("name", In, "alpha", "gamma").
		Execute(context.Background())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
}

func TestSelect_WhereIsNull(t *testing.T) {
	ds := integrationDS(t)
	setupDB(t, ds)
	// category has a DEFAULT '' so it's never NULL in this schema; use score
	// comparison instead to exercise IsNull path indirectly via a nullable view.
	// We simply verify the query builds and runs without error.
	repo := NewRepository[testItem](ds, "test_repo_items")
	_, err := repo.Select().Where("category", IsNull).Execute(context.Background())
	if err != nil {
		t.Fatalf("Execute with IsNull: %v", err)
	}
}

func TestSelect_OrderByAsc(t *testing.T) {
	ds := integrationDS(t)
	setupDB(t, ds)

	insertItem(t, ds, "c", 30, "x")
	insertItem(t, ds, "a", 10, "x")
	insertItem(t, ds, "b", 20, "x")

	repo := NewRepository[testItem](ds, "test_repo_items")
	rows, err := repo.Select().OrderBy("score", ASC).Execute(context.Background())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(rows) != 3 || rows[0].Score != 10 || rows[2].Score != 30 {
		t.Fatalf("unexpected order: %v", rows)
	}
}

func TestSelect_OrderByDesc(t *testing.T) {
	ds := integrationDS(t)
	setupDB(t, ds)

	insertItem(t, ds, "a", 10, "x")
	insertItem(t, ds, "b", 30, "x")

	repo := NewRepository[testItem](ds, "test_repo_items")
	rows, err := repo.Select().OrderBy("score", DESC).Execute(context.Background())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(rows) != 2 || rows[0].Score != 30 {
		t.Fatalf("unexpected order: %v", rows)
	}
}

func TestSelect_Limit(t *testing.T) {
	ds := integrationDS(t)
	setupDB(t, ds)

	for i := 0; i < 5; i++ {
		insertItem(t, ds, "item", i, "x")
	}

	repo := NewRepository[testItem](ds, "test_repo_items")
	rows, err := repo.Select().Limit(3).Execute(context.Background())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}
}

func TestSelect_WhereOnJoinedTable(t *testing.T) {
	ds := integrationDS(t)
	setupDB(t, ds)

	// Add a tag and reference it by category name (simulate joined filter
	// using a qualified column that bypasses property validation).
	insertItem(t, ds, "joined", 5, "go")
	insertItem(t, ds, "other", 5, "rust")
	insertTag(t, ds, "go")

	repo := NewRepository[testItem](ds, "test_repo_items")
	rows, err := repo.Select().
		Join("test_repo_tags", "test_repo_items.category", Equals, "test_repo_tags.label").
		Where("test_repo_tags.label", Equals, "go").
		Execute(context.Background())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(rows) != 1 || rows[0].Name != "joined" {
		t.Fatalf("unexpected rows: %v", rows)
	}
}

func TestSelect_LeftJoin(t *testing.T) {
	ds := integrationDS(t)
	setupDB(t, ds)

	insertItem(t, ds, "with-tag", 1, "go")
	insertItem(t, ds, "no-tag", 2, "python")
	insertTag(t, ds, "go")

	repo := NewRepository[testItem](ds, "test_repo_items")
	rows, err := repo.Select().
		LeftJoin("test_repo_tags", "test_repo_items.category", Equals, "test_repo_tags.label").
		Execute(context.Background())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// LEFT JOIN must return all items (both with and without matching tag).
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
}

// ---------------------------------------------------------------------------
// Insert tests
// ---------------------------------------------------------------------------

func TestInsert_SingleRow(t *testing.T) {
	ds := integrationDS(t)
	setupDB(t, ds)

	repo := NewRepository[testItem](ds, "test_repo_items")
	affected, err := repo.Insert().
		Set("name", "alice").Set("score", 42).Set("category", "eng").
		Execute(context.Background())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if affected != 1 {
		t.Fatalf("rows affected = %d, want 1", affected)
	}
}

func TestInsert_RowReadBack(t *testing.T) {
	ds := integrationDS(t)
	setupDB(t, ds)

	repo := NewRepository[testItem](ds, "test_repo_items")
	if _, err := repo.Insert().
		Set("name", "bob").Set("score", 7).Set("category", "ops").
		Execute(context.Background()); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	rows, err := repo.Select().Where("name", Equals, "bob").Execute(context.Background())
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if len(rows) != 1 || rows[0].Score != 7 || rows[0].Category != "ops" {
		t.Fatalf("unexpected row: %v", rows)
	}
}

func TestInsert_MultipleRows(t *testing.T) {
	ds := integrationDS(t)
	setupDB(t, ds)

	repo := NewRepository[testItem](ds, "test_repo_items")
	for i, name := range []string{"x", "y", "z"} {
		if _, err := repo.Insert().Set("name", name).Set("score", i).Execute(context.Background()); err != nil {
			t.Fatalf("Insert %q: %v", name, err)
		}
	}

	rows, err := repo.Select().Execute(context.Background())
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}
}

// ---------------------------------------------------------------------------
// Update tests
// ---------------------------------------------------------------------------

func TestUpdate_SetOneColumn(t *testing.T) {
	ds := integrationDS(t)
	setupDB(t, ds)

	insertItem(t, ds, "target", 0, "x")

	repo := NewRepository[testItem](ds, "test_repo_items")
	affected, err := repo.Update().
		Set("score", 99).
		Where("name", Equals, "target").
		Execute(context.Background())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if affected != 1 {
		t.Fatalf("rows affected = %d, want 1", affected)
	}

	rows, _ := repo.Select().Where("name", Equals, "target").Execute(context.Background())
	if rows[0].Score != 99 {
		t.Fatalf("score = %d, want 99", rows[0].Score)
	}
}

func TestUpdate_SetMultipleColumns(t *testing.T) {
	ds := integrationDS(t)
	setupDB(t, ds)

	insertItem(t, ds, "multi", 1, "old")

	repo := NewRepository[testItem](ds, "test_repo_items")
	_, err := repo.Update().
		Set("score", 55).
		Set("category", "new").
		Where("name", Equals, "multi").
		Execute(context.Background())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	rows, _ := repo.Select().Where("name", Equals, "multi").Execute(context.Background())
	if rows[0].Score != 55 || rows[0].Category != "new" {
		t.Fatalf("unexpected row: score=%d category=%s", rows[0].Score, rows[0].Category)
	}
}

func TestUpdate_NoMatchingRows(t *testing.T) {
	ds := integrationDS(t)
	setupDB(t, ds)

	repo := NewRepository[testItem](ds, "test_repo_items")
	affected, err := repo.Update().
		Set("score", 1).
		Where("name", Equals, "ghost").
		Execute(context.Background())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if affected != 0 {
		t.Fatalf("rows affected = %d, want 0", affected)
	}
}

func TestUpdate_NoSetClausesError(t *testing.T) {
	ds := integrationDS(t)
	setupDB(t, ds)

	repo := NewRepository[testItem](ds, "test_repo_items")
	_, err := repo.Update().Execute(context.Background())
	if err == nil {
		t.Fatal("expected error for UPDATE with no SET clauses, got nil")
	}
}

func TestUpdate_InvalidProperty(t *testing.T) {
	ds := integrationDS(t)
	setupDB(t, ds)

	repo := NewRepository[testItem](ds, "test_repo_items")
	_, err := repo.Update().Set("nonexistent", 1).Execute(context.Background())
	if err == nil {
		t.Fatal("expected error for unknown property, got nil")
	}
}

func TestUpdate_NoWhereUpdatesAll(t *testing.T) {
	ds := integrationDS(t)
	setupDB(t, ds)

	insertItem(t, ds, "a", 1, "x")
	insertItem(t, ds, "b", 2, "x")

	repo := NewRepository[testItem](ds, "test_repo_items")
	affected, err := repo.Update().Set("category", "updated").Execute(context.Background())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if affected != 2 {
		t.Fatalf("rows affected = %d, want 2", affected)
	}
}

func TestUpdate_OrWhere(t *testing.T) {
	ds := integrationDS(t)
	setupDB(t, ds)

	insertItem(t, ds, "alpha", 10, "x")
	insertItem(t, ds, "beta", 20, "x")
	insertItem(t, ds, "gamma", 30, "x")

	repo := NewRepository[testItem](ds, "test_repo_items")
	// UPDATE … SET category='y' WHERE name='alpha' OR name='gamma'
	affected, err := repo.Update().
		Set("category", "y").
		Where("name", Equals, "alpha").
		OrWhere("name", Equals, "gamma").
		Execute(context.Background())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if affected != 2 {
		t.Fatalf("rows affected = %d, want 2", affected)
	}

	rows, _ := repo.Select().Where("category", Equals, "y").Execute(context.Background())
	if len(rows) != 2 {
		t.Fatalf("got %d updated rows, want 2", len(rows))
	}
	// "beta" must be untouched
	untouched, _ := repo.Select().Where("name", Equals, "beta").Execute(context.Background())
	if len(untouched) != 1 || untouched[0].Category != "x" {
		t.Fatalf("beta should not have been updated: %+v", untouched)
	}
}

func TestUpdate_OrWhereWithAnd(t *testing.T) {
	ds := integrationDS(t)
	setupDB(t, ds)

	insertItem(t, ds, "alpha", 10, "keep")
	insertItem(t, ds, "beta", 20, "keep")
	insertItem(t, ds, "gamma", 30, "keep")

	repo := NewRepository[testItem](ds, "test_repo_items")
	// WHERE (name='alpha' OR name='gamma') AND score > 15  → only gamma matches
	affected, err := repo.Update().
		Set("category", "changed").
		Where("name", Equals, "alpha").
		OrWhere("name", Equals, "gamma").
		Where("score", Greeter, 15).
		Execute(context.Background())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if affected != 1 {
		t.Fatalf("rows affected = %d, want 1", affected)
	}

	rows, _ := repo.Select().Where("name", Equals, "gamma").Execute(context.Background())
	if len(rows) != 1 || rows[0].Category != "changed" {
		t.Fatalf("expected gamma.category=changed, got %+v", rows)
	}
}

// ---------------------------------------------------------------------------
// Delete tests
// ---------------------------------------------------------------------------

func TestDelete_WithWhere(t *testing.T) {
	ds := integrationDS(t)
	setupDB(t, ds)

	insertItem(t, ds, "keep", 1, "x")
	insertItem(t, ds, "remove", 2, "x")

	repo := NewRepository[testItem](ds, "test_repo_items")
	affected, err := repo.Delete().Where("name", Equals, "remove").Execute(context.Background())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if affected != 1 {
		t.Fatalf("rows affected = %d, want 1", affected)
	}

	rows, _ := repo.Select().Execute(context.Background())
	if len(rows) != 1 || rows[0].Name != "keep" {
		t.Fatalf("unexpected rows after delete: %v", rows)
	}
}

func TestDelete_NoWhere_DeletesAll(t *testing.T) {
	ds := integrationDS(t)
	setupDB(t, ds)

	insertItem(t, ds, "a", 1, "x")
	insertItem(t, ds, "b", 2, "x")

	repo := NewRepository[testItem](ds, "test_repo_items")
	affected, err := repo.Delete().Execute(context.Background())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if affected != 2 {
		t.Fatalf("rows affected = %d, want 2", affected)
	}
}

func TestDelete_NoMatchingRows(t *testing.T) {
	ds := integrationDS(t)
	setupDB(t, ds)

	insertItem(t, ds, "real", 1, "x")

	repo := NewRepository[testItem](ds, "test_repo_items")
	affected, err := repo.Delete().Where("name", Equals, "ghost").Execute(context.Background())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if affected != 0 {
		t.Fatalf("rows affected = %d, want 0", affected)
	}
}

func TestDelete_EmptyTable(t *testing.T) {
	ds := integrationDS(t)
	setupDB(t, ds)

	repo := NewRepository[testItem](ds, "test_repo_items")
	affected, err := repo.Delete().Execute(context.Background())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if affected != 0 {
		t.Fatalf("rows affected = %d, want 0", affected)
	}
}

func TestDelete_WhereIn(t *testing.T) {
	ds := integrationDS(t)
	setupDB(t, ds)

	insertItem(t, ds, "a", 1, "x")
	insertItem(t, ds, "b", 2, "x")
	insertItem(t, ds, "c", 3, "x")

	repo := NewRepository[testItem](ds, "test_repo_items")
	affected, err := repo.Delete().
		Where("name", In, "a", "c").
		Execute(context.Background())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if affected != 2 {
		t.Fatalf("rows affected = %d, want 2", affected)
	}

	rows, _ := repo.Select().Execute(context.Background())
	if len(rows) != 1 || rows[0].Name != "b" {
		t.Fatalf("unexpected remaining rows: %v", rows)
	}
}

func TestDelete_OrWhere(t *testing.T) {
	ds := integrationDS(t)
	setupDB(t, ds)

	insertItem(t, ds, "alpha", 10, "x")
	insertItem(t, ds, "beta", 20, "x")
	insertItem(t, ds, "gamma", 30, "x")

	repo := NewRepository[testItem](ds, "test_repo_items")
	// DELETE … WHERE name='alpha' OR name='gamma'
	affected, err := repo.Delete().
		Where("name", Equals, "alpha").
		OrWhere("name", Equals, "gamma").
		Execute(context.Background())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if affected != 2 {
		t.Fatalf("rows affected = %d, want 2", affected)
	}

	rows, _ := repo.Select().Execute(context.Background())
	if len(rows) != 1 || rows[0].Name != "beta" {
		t.Fatalf("unexpected remaining rows: %v", rows)
	}
}

func TestDelete_OrWhereWithAnd(t *testing.T) {
	ds := integrationDS(t)
	setupDB(t, ds)

	insertItem(t, ds, "alpha", 10, "keep")
	insertItem(t, ds, "beta", 20, "keep")
	insertItem(t, ds, "gamma", 30, "remove")

	repo := NewRepository[testItem](ds, "test_repo_items")
	// WHERE (name='alpha' OR name='gamma') AND category='remove' → only gamma
	affected, err := repo.Delete().
		Where("name", Equals, "alpha").
		OrWhere("name", Equals, "gamma").
		Where("category", Equals, "remove").
		Execute(context.Background())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if affected != 1 {
		t.Fatalf("rows affected = %d, want 1", affected)
	}

	rows, _ := repo.Select().Execute(context.Background())
	if len(rows) != 2 {
		t.Fatalf("got %d remaining rows, want 2", len(rows))
	}
	for _, r := range rows {
		if r.Name == "gamma" {
			t.Fatal("gamma should have been deleted")
		}
	}
}

func TestDelete_MultipleOrGroups(t *testing.T) {
	ds := integrationDS(t)
	setupDB(t, ds)

	insertItem(t, ds, "alpha", 10, "a")
	insertItem(t, ds, "beta", 20, "b")
	insertItem(t, ds, "gamma", 30, "a")
	insertItem(t, ds, "delta", 40, "b")

	repo := NewRepository[testItem](ds, "test_repo_items")
	// WHERE (name='alpha' OR name='beta') AND (category='a' OR category='b')
	// All four rows match, but only alpha+gamma have category 'a' and
	// only beta+delta have category 'b'; combined the AND of two OR groups
	// selects all four rows.
	affected, err := repo.Delete().
		Where("name", Equals, "alpha").
		OrWhere("name", Equals, "beta").
		Where("category", Equals, "a").
		OrWhere("category", Equals, "b").
		Execute(context.Background())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// (alpha OR beta) AND (a OR b):
	// alpha→a ✓, beta→b ✓ → 2 rows deleted
	if affected != 2 {
		t.Fatalf("rows affected = %d, want 2", affected)
	}

	remaining, _ := repo.Select().Execute(context.Background())
	if len(remaining) != 2 {
		t.Fatalf("got %d remaining rows, want 2", len(remaining))
	}
}

// ---------------------------------------------------------------------------
// Select — additional operator coverage
// ---------------------------------------------------------------------------

func TestSelect_WhereGt(t *testing.T) {
	ds := integrationDS(t)
	setupDB(t, ds)

	insertItem(t, ds, "low", 5, "x")
	insertItem(t, ds, "mid", 10, "x")
	insertItem(t, ds, "high", 20, "x")

	repo := NewRepository[testItem](ds, "test_repo_items")
	rows, err := repo.Select().Where("score", Greeter, 5).Execute(context.Background())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
}

func TestSelect_WhereLt(t *testing.T) {
	ds := integrationDS(t)
	setupDB(t, ds)

	insertItem(t, ds, "a", 1, "x")
	insertItem(t, ds, "b", 5, "x")
	insertItem(t, ds, "c", 10, "x")

	repo := NewRepository[testItem](ds, "test_repo_items")
	rows, err := repo.Select().Where("score", Less, 5).Execute(context.Background())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(rows) != 1 || rows[0].Name != "a" {
		t.Fatalf("unexpected rows: %v", rows)
	}
}

func TestSelect_WhereGte(t *testing.T) {
	ds := integrationDS(t)
	setupDB(t, ds)

	insertItem(t, ds, "a", 5, "x")
	insertItem(t, ds, "b", 10, "x")
	insertItem(t, ds, "c", 15, "x")

	repo := NewRepository[testItem](ds, "test_repo_items")
	rows, err := repo.Select().Where("score", GreeterOrEqual, 10).Execute(context.Background())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
}

func TestSelect_WhereLte(t *testing.T) {
	ds := integrationDS(t)
	setupDB(t, ds)

	insertItem(t, ds, "a", 5, "x")
	insertItem(t, ds, "b", 10, "x")
	insertItem(t, ds, "c", 15, "x")

	repo := NewRepository[testItem](ds, "test_repo_items")
	rows, err := repo.Select().Where("score", LessOrEqual, 10).Execute(context.Background())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
}

func TestSelect_WhereNotEq(t *testing.T) {
	ds := integrationDS(t)
	setupDB(t, ds)

	insertItem(t, ds, "alpha", 1, "x")
	insertItem(t, ds, "beta", 2, "x")

	repo := NewRepository[testItem](ds, "test_repo_items")
	rows, err := repo.Select().Where("name", NotEquals, "alpha").Execute(context.Background())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(rows) != 1 || rows[0].Name != "beta" {
		t.Fatalf("unexpected rows: %v", rows)
	}
}

func TestSelect_WhereLike(t *testing.T) {
	ds := integrationDS(t)
	setupDB(t, ds)

	insertItem(t, ds, "foobar", 1, "x")
	insertItem(t, ds, "foobaz", 2, "x")
	insertItem(t, ds, "other", 3, "x")

	repo := NewRepository[testItem](ds, "test_repo_items")
	rows, err := repo.Select().Where("name", Like, "foo%").Execute(context.Background())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
}

func TestSelect_WhereILike(t *testing.T) {
	ds := integrationDS(t)
	setupDB(t, ds)

	insertItem(t, ds, "FooBar", 1, "x")
	insertItem(t, ds, "foobar", 2, "x")
	insertItem(t, ds, "other", 3, "x")

	repo := NewRepository[testItem](ds, "test_repo_items")
	rows, err := repo.Select().Where("name", ILike, "foo%").Execute(context.Background())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
}

func TestSelect_WhereNotIn(t *testing.T) {
	ds := integrationDS(t)
	setupDB(t, ds)

	insertItem(t, ds, "a", 1, "x")
	insertItem(t, ds, "b", 2, "x")
	insertItem(t, ds, "c", 3, "x")

	repo := NewRepository[testItem](ds, "test_repo_items")
	rows, err := repo.Select().
		Where("name", NotIn, "a", "c").
		Execute(context.Background())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(rows) != 1 || rows[0].Name != "b" {
		t.Fatalf("unexpected rows: %v", rows)
	}
}

func TestSelect_WhereIsNotNull(t *testing.T) {
	ds := integrationDS(t)
	setupDB(t, ds)

	insertItem(t, ds, "present", 1, "x")

	repo := NewRepository[testItem](ds, "test_repo_items")
	rows, err := repo.Select().Where("name", IsNotNull).Execute(context.Background())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
}

func TestSelect_AliasedJoin(t *testing.T) {
	ds := integrationDS(t)
	setupDB(t, ds)

	// items have two tags referenced via category; use alias to join same table twice
	insertItem(t, ds, "item", 1, "go")
	insertTag(t, ds, "go")
	insertTag(t, ds, "rust")

	repo := NewRepository[testItem](ds, "test_repo_items")
	rows, err := repo.Select().
		Join("test_repo_tags", "test_repo_items.category", Equals, "t1.label", "t1").
		Where("t1.label", Equals, "go").
		Execute(context.Background())
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(rows) != 1 || rows[0].Name != "item" {
		t.Fatalf("unexpected rows: %v", rows)
	}
}

// ---------------------------------------------------------------------------
// Insert — validation edge cases
// ---------------------------------------------------------------------------

func TestInsert_NoSetsError(t *testing.T) {
	ds := integrationDS(t)
	setupDB(t, ds)

	repo := NewRepository[testItem](ds, "test_repo_items")
	_, err := repo.Insert().Execute(context.Background())
	if err == nil {
		t.Fatal("expected error for INSERT with no columns, got nil")
	}
}

func TestInsert_InvalidProperty(t *testing.T) {
	ds := integrationDS(t)
	setupDB(t, ds)

	repo := NewRepository[testItem](ds, "test_repo_items")
	_, err := repo.Insert().Set("nonexistent", "x").Execute(context.Background())
	if err == nil {
		t.Fatal("expected error for unknown property, got nil")
	}
}

func TestInsert_DuplicateColumns(t *testing.T) {
	ds := integrationDS(t)
	setupDB(t, ds)

	repo := NewRepository[testItem](ds, "test_repo_items")
	_, err := repo.Insert().
		Set("name", "first").Set("name", "second").
		Execute(context.Background())
	// PostgreSQL raises an error for duplicate column in INSERT
	if err == nil {
		t.Fatal("expected error for duplicate column in INSERT, got nil")
	}
}

// ---------------------------------------------------------------------------
// UpdateOnConflict tests
// ---------------------------------------------------------------------------

func TestInsert_UpdateOnConflict_Updates(t *testing.T) {
	ds := integrationDS(t)
	setupDB(t, ds)

	// test_repo_items has a unique constraint only on the serial PK (id).
	// Use name as a de-facto unique key via a unique index created here.
	ds.Exec(context.Background(), //nolint:errcheck
		"CREATE UNIQUE INDEX IF NOT EXISTS uq_test_repo_items_name ON test_repo_items (name)")
	t.Cleanup(func() {
		ds.Exec(context.Background(), "DROP INDEX IF EXISTS uq_test_repo_items_name") //nolint:errcheck
	})

	repo := NewRepository[testItem](ds, "test_repo_items")
	// First insert.
	if _, err := repo.Insert().
		Set("name", "alice").Set("score", 10).Set("category", "eng").
		Execute(context.Background()); err != nil {
		t.Fatalf("first insert: %v", err)
	}

	// Second insert with conflict on name → update score and category.
	affected, err := repo.Insert().
		Set("name", "alice").Set("score", 99).Set("category", "ops").
		UpdateOnConflict([]Property{"name"}, "score", "category").
		Execute(context.Background())
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if affected != 1 {
		t.Fatalf("rows affected = %d, want 1", affected)
	}

	rows, err := repo.Select().Where("name", Equals, "alice").Execute(context.Background())
	if err != nil {
		t.Fatalf("select after upsert: %v", err)
	}
	if len(rows) != 1 || rows[0].Score != 99 || rows[0].Category != "ops" {
		t.Fatalf("unexpected row after upsert: %+v", rows)
	}
}

func TestInsert_UpdateOnConflict_NoConflict(t *testing.T) {
	ds := integrationDS(t)
	setupDB(t, ds)

	ds.Exec(context.Background(), //nolint:errcheck
		"CREATE UNIQUE INDEX IF NOT EXISTS uq_test_repo_items_name ON test_repo_items (name)")
	t.Cleanup(func() {
		ds.Exec(context.Background(), "DROP INDEX IF EXISTS uq_test_repo_items_name") //nolint:errcheck
	})

	repo := NewRepository[testItem](ds, "test_repo_items")
	// No prior row — insert should succeed normally.
	affected, err := repo.Insert().
		Set("name", "bob").Set("score", 5).Set("category", "eng").
		UpdateOnConflict([]Property{"name"}, "score", "category").
		Execute(context.Background())
	if err != nil {
		t.Fatalf("upsert (no conflict): %v", err)
	}
	if affected != 1 {
		t.Fatalf("rows affected = %d, want 1", affected)
	}
}

func TestInsert_UpdateOnConflict_InvalidConflictCol(t *testing.T) {
	ds := integrationDS(t)
	setupDB(t, ds)

	repo := NewRepository[testItem](ds, "test_repo_items")
	_, err := repo.Insert().
		Set("name", "x").
		UpdateOnConflict([]Property{"nonexistent"}, "score").
		Execute(context.Background())
	if err == nil {
		t.Fatal("expected error for unknown conflict column, got nil")
	}
}

func TestInsert_UpdateOnConflict_InvalidUpdateCol(t *testing.T) {
	ds := integrationDS(t)
	setupDB(t, ds)

	repo := NewRepository[testItem](ds, "test_repo_items")
	_, err := repo.Insert().
		Set("name", "x").
		UpdateOnConflict([]Property{"name"}, "nonexistent").
		Execute(context.Background())
	if err == nil {
		t.Fatal("expected error for unknown update column, got nil")
	}
}

// ---------------------------------------------------------------------------
// Count tests
// ---------------------------------------------------------------------------

func TestCount_EmptyTable(t *testing.T) {
	ds := integrationDS(t)
	setupDB(t, ds)

	repo := NewRepository[testItem](ds, "test_repo_items")
	count, err := repo.Select().Count(context.Background())
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if count != 0 {
		t.Fatalf("got %d, want 0", count)
	}
}

func TestCount_AllRows(t *testing.T) {
	ds := integrationDS(t)
	setupDB(t, ds)

	insertItem(t, ds, "a", 1, "x")
	insertItem(t, ds, "b", 2, "x")
	insertItem(t, ds, "c", 3, "x")

	repo := NewRepository[testItem](ds, "test_repo_items")
	count, err := repo.Select().Count(context.Background())
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if count != 3 {
		t.Fatalf("got %d, want 3", count)
	}
}

func TestCount_WithWhere(t *testing.T) {
	ds := integrationDS(t)
	setupDB(t, ds)

	insertItem(t, ds, "a", 1, "eng")
	insertItem(t, ds, "b", 2, "ops")
	insertItem(t, ds, "c", 3, "eng")

	repo := NewRepository[testItem](ds, "test_repo_items")
	count, err := repo.Select().Where("category", Equals, "eng").Count(context.Background())
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if count != 2 {
		t.Fatalf("got %d, want 2", count)
	}
}

func TestCount_WithMultipleWheres(t *testing.T) {
	ds := integrationDS(t)
	setupDB(t, ds)

	insertItem(t, ds, "a", 10, "eng")
	insertItem(t, ds, "b", 5, "eng")
	insertItem(t, ds, "c", 10, "ops")

	repo := NewRepository[testItem](ds, "test_repo_items")
	count, err := repo.Select().
		Where("category", Equals, "eng").
		Where("score", GreeterOrEqual, 10).
		Count(context.Background())
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if count != 1 {
		t.Fatalf("got %d, want 1", count)
	}
}

func TestCount_WithWhereIn(t *testing.T) {
	ds := integrationDS(t)
	setupDB(t, ds)

	insertItem(t, ds, "a", 1, "x")
	insertItem(t, ds, "b", 2, "x")
	insertItem(t, ds, "c", 3, "x")

	repo := NewRepository[testItem](ds, "test_repo_items")
	count, err := repo.Select().
		Where("name", In, "a", "c").
		Count(context.Background())
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if count != 2 {
		t.Fatalf("got %d, want 2", count)
	}
}

func TestCount_WithJoin(t *testing.T) {
	ds := integrationDS(t)
	setupDB(t, ds)

	insertItem(t, ds, "matched", 1, "go")
	insertItem(t, ds, "unmatched", 2, "rust")
	insertTag(t, ds, "go")

	repo := NewRepository[testItem](ds, "test_repo_items")
	count, err := repo.Select().
		Join("test_repo_tags", "test_repo_items.category", Equals, "test_repo_tags.label").
		Count(context.Background())
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if count != 1 {
		t.Fatalf("got %d, want 1", count)
	}
}

func TestCount_IgnoresOrderByAndLimit(t *testing.T) {
	ds := integrationDS(t)
	setupDB(t, ds)

	for i := 0; i < 5; i++ {
		insertItem(t, ds, "item", i, "x")
	}

	repo := NewRepository[testItem](ds, "test_repo_items")
	// OrderBy and Limit must not affect COUNT(*).
	count, err := repo.Select().OrderBy("score", DESC).Limit(2).Count(context.Background())
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if count != 5 {
		t.Fatalf("got %d, want 5 (Limit must be ignored by Count)", count)
	}
}

// ---------------------------------------------------------------------------
// Exists tests
// ---------------------------------------------------------------------------

func TestExists_EmptyTable(t *testing.T) {
	ds := integrationDS(t)
	setupDB(t, ds)

	repo := NewRepository[testItem](ds, "test_repo_items")
	exists, err := repo.Select().Exists(context.Background())
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if exists {
		t.Fatal("got true, want false for empty table")
	}
}

func TestExists_RowPresent(t *testing.T) {
	ds := integrationDS(t)
	setupDB(t, ds)

	insertItem(t, ds, "alpha", 10, "x")

	repo := NewRepository[testItem](ds, "test_repo_items")
	exists, err := repo.Select().Exists(context.Background())
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if !exists {
		t.Fatal("got false, want true")
	}
}

func TestExists_WithMatchingWhere(t *testing.T) {
	ds := integrationDS(t)
	setupDB(t, ds)

	insertItem(t, ds, "alpha", 10, "x")
	insertItem(t, ds, "beta", 20, "y")

	repo := NewRepository[testItem](ds, "test_repo_items")
	exists, err := repo.Select().Where("name", Equals, "alpha").Exists(context.Background())
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if !exists {
		t.Fatal("got false, want true")
	}
}

func TestExists_WithNonMatchingWhere(t *testing.T) {
	ds := integrationDS(t)
	setupDB(t, ds)

	insertItem(t, ds, "alpha", 10, "x")

	repo := NewRepository[testItem](ds, "test_repo_items")
	exists, err := repo.Select().Where("name", Equals, "ghost").Exists(context.Background())
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if exists {
		t.Fatal("got true, want false")
	}
}

func TestExists_WithOrWhere(t *testing.T) {
	ds := integrationDS(t)
	setupDB(t, ds)

	insertItem(t, ds, "alpha", 10, "x")
	insertItem(t, ds, "beta", 20, "x")

	repo := NewRepository[testItem](ds, "test_repo_items")
	// Neither "ghost" nor "phantom" exist — should be false.
	exists, err := repo.Select().
		Where("name", Equals, "ghost").
		OrWhere("name", Equals, "phantom").
		Exists(context.Background())
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if exists {
		t.Fatal("got true, want false")
	}

	// "alpha" or "beta" both exist — should be true.
	exists, err = repo.Select().
		Where("name", Equals, "alpha").
		OrWhere("name", Equals, "beta").
		Exists(context.Background())
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if !exists {
		t.Fatal("got false, want true")
	}
}

func TestExists_WithJoin(t *testing.T) {
	ds := integrationDS(t)
	setupDB(t, ds)

	insertItem(t, ds, "matched", 1, "go")
	insertItem(t, ds, "unmatched", 2, "rust")
	insertTag(t, ds, "go")

	repo := NewRepository[testItem](ds, "test_repo_items")
	exists, err := repo.Select().
		Join("test_repo_tags", "test_repo_items.category", Equals, "test_repo_tags.label").
		Where("test_repo_tags.label", Equals, "go").
		Exists(context.Background())
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if !exists {
		t.Fatal("got false, want true")
	}
}

func TestExists_IgnoresOrderByAndLimit(t *testing.T) {
	ds := integrationDS(t)
	setupDB(t, ds)

	insertItem(t, ds, "a", 1, "x")
	insertItem(t, ds, "b", 2, "x")

	repo := NewRepository[testItem](ds, "test_repo_items")
	exists, err := repo.Select().OrderBy("score", DESC).Limit(1).Exists(context.Background())
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if !exists {
		t.Fatal("got false, want true")
	}
}
