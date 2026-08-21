package functional

import (
	"strings"
	"testing"
)

func TestViewBuildSQL(t *testing.T) {
	sql, err := NewView("public.active_users").
		OrReplace().
		As("SELECT id, email FROM users WHERE active").
		WithCheckOption(LocalCheckOption).
		BuildSQL()
	if err != nil {
		t.Fatalf("BuildSQL: %v", err)
	}
	for _, want := range []string{
		`CREATE OR REPLACE VIEW "public"."active_users" AS`,
		`SELECT id, email FROM users WHERE active`,
		`WITH LOCAL CHECK OPTION;`,
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("SQL missing %q:\n%s", want, sql)
		}
	}
}

func TestViewDropSQL(t *testing.T) {
	sql, err := NewView("public.active_users").DropSQL(true)
	if err != nil {
		t.Fatalf("DropSQL: %v", err)
	}
	if sql != `DROP VIEW IF EXISTS "public"."active_users";` {
		t.Fatalf("unexpected SQL: %s", sql)
	}
}

func TestMaterializedViewBuildSQL(t *testing.T) {
	sql, err := NewMaterializedView("public.user_totals").
		IfNotExists().
		WithStorageParameter("fillfactor", "80").
		InTablespace("fast_space").
		As("SELECT user_id, count(*) AS total FROM orders GROUP BY user_id").
		WithNoData().
		BuildSQL()
	if err != nil {
		t.Fatalf("BuildSQL: %v", err)
	}
	for _, want := range []string{
		`CREATE MATERIALIZED VIEW IF NOT EXISTS "public"."user_totals"`,
		`WITH (fillfactor = 80)`,
		`TABLESPACE "fast_space"`,
		`SELECT user_id, count(*) AS total FROM orders GROUP BY user_id`,
		`WITH NO DATA;`,
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("SQL missing %q:\n%s", want, sql)
		}
	}
}

func TestMaterializedViewRefreshSQL(t *testing.T) {
	sql, err := NewMaterializedView("public.user_totals").RefreshSQL(true, true)
	if err != nil {
		t.Fatalf("RefreshSQL: %v", err)
	}
	if sql != `REFRESH MATERIALIZED VIEW CONCURRENTLY "public"."user_totals" WITH DATA;` {
		t.Fatalf("unexpected SQL: %s", sql)
	}
}

func TestFunctionBuildSQL(t *testing.T) {
	sql, err := NewFunction("public.normalize_email").
		OrReplace().
		WithArguments("value text").
		Returns("text").
		Language("sql").
		WithVolatility(Immutable).
		Strict().
		Body("SELECT lower(trim(value))").
		BuildSQL()
	if err != nil {
		t.Fatalf("BuildSQL: %v", err)
	}
	for _, want := range []string{
		`CREATE OR REPLACE FUNCTION "public"."normalize_email"(value text)`,
		`RETURNS text`,
		`LANGUAGE sql`,
		`IMMUTABLE`,
		`STRICT`,
		`SELECT lower(trim(value))`,
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("SQL missing %q:\n%s", want, sql)
		}
	}
}

func TestFunctionDropSQL(t *testing.T) {
	sql, err := NewFunction("public.normalize_email").
		WithArguments("value text").
		DropSQL(true)
	if err != nil {
		t.Fatalf("DropSQL: %v", err)
	}
	if sql != `DROP FUNCTION IF EXISTS "public"."normalize_email"(value text);` {
		t.Fatalf("unexpected SQL: %s", sql)
	}
}

func TestFunctionBuildSQL_UsesSafeDollarQuote(t *testing.T) {
	sql, err := NewFunction("public.echo").
		Returns("text").
		Body("BEGIN\nRETURN $$quoted$$;\nEND").
		BuildSQL()
	if err != nil {
		t.Fatalf("BuildSQL: %v", err)
	}
	if !strings.Contains(sql, "$pgxext0$") {
		t.Fatalf("SQL does not use alternate dollar quote:\n%s", sql)
	}
}

func TestRejectsInvalidQualifiedName(t *testing.T) {
	_, err := NewView("public.users;drop").As("SELECT 1").BuildSQL()
	if err == nil {
		t.Fatal("expected invalid name error, got nil")
	}
}
