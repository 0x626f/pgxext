package notification

import (
	"strings"
	"testing"
)

func TestNotification_BuildSQL_DefaultEmptyPayload(t *testing.T) {
	sql, err := NewNotification("users", "user_changed").BuildSQL()
	if err != nil {
		t.Fatalf("BuildSQL: %v", err)
	}
	for _, want := range []string{
		`payload := '{}'::jsonb;`,
		`PERFORM pg_notify('user_changed', payload::text);`,
		`AFTER INSERT OR UPDATE OR DELETE ON "users"`,
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("SQL missing %q:\n%s", want, sql)
		}
	}
}

func TestNotification_BuildSQL_WithPayloadProperties(t *testing.T) {
	sql, err := NewNotification("public.users", "user_changed").
		On(Insert, Update).
		WithPayloadProperties(FromState, ToState, TableName, RowID, CreatedAt).
		BuildSQL()
	if err != nil {
		t.Fatalf("BuildSQL: %v", err)
	}
	for _, want := range []string{
		`jsonb_build_object(`,
		`'fromState', CASE WHEN TG_OP IN ('UPDATE', 'DELETE') THEN to_jsonb(OLD) ELSE NULL END`,
		`'toState', CASE WHEN TG_OP IN ('INSERT', 'UPDATE') THEN to_jsonb(NEW) ELSE NULL END`,
		`'tableName', TG_TABLE_NAME`,
		`'rowId', CASE WHEN TG_OP = 'DELETE' THEN to_jsonb(OLD)->'id' ELSE to_jsonb(NEW)->'id' END`,
		`'createdAt', to_jsonb(clock_timestamp())`,
		`AFTER INSERT OR UPDATE ON "public"."users"`,
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("SQL missing %q:\n%s", want, sql)
		}
	}
}

func TestNotification_BuildSQL_WithCustomRowIDColumn(t *testing.T) {
	sql, err := NewNotification("users", "user_changed").
		WithRowIDColumn("uuid").
		WithPayloadProperties(RowID).
		BuildSQL()
	if err != nil {
		t.Fatalf("BuildSQL: %v", err)
	}
	if !strings.Contains(sql, `to_jsonb(NEW)->'uuid'`) {
		t.Fatalf("SQL does not use custom row id column:\n%s", sql)
	}
}

func TestNotification_BuildSQL_AllowsCustomQuotedEventName(t *testing.T) {
	sql, err := NewNotification("users", "user.changed").BuildSQL()
	if err != nil {
		t.Fatalf("BuildSQL: %v", err)
	}
	if !strings.Contains(sql, `pg_notify('user.changed'`) {
		t.Fatalf("SQL does not use custom event name:\n%s", sql)
	}
}

func TestNotification_BuildSQL_RejectsInvalidTable(t *testing.T) {
	_, err := NewNotification("users;drop", "user_changed").BuildSQL()
	if err == nil {
		t.Fatal("expected error for invalid table identifier, got nil")
	}
}

func TestNotification_BuildSQL_RejectsInvalidRowIDColumn(t *testing.T) {
	_, err := NewNotification("users", "user_changed").
		WithRowIDColumn("id;drop").
		WithPayloadProperties(RowID).
		BuildSQL()
	if err == nil {
		t.Fatal("expected error for invalid row id identifier, got nil")
	}
}

func TestNotification_DropSQL(t *testing.T) {
	sql, err := NewNotification("public.users", "user_changed").DropSQL()
	if err != nil {
		t.Fatalf("DropSQL: %v", err)
	}
	for _, want := range []string{
		`DROP TRIGGER IF EXISTS "pgxext_notify_public_users_user_changed_trg" ON "public"."users";`,
		`DROP FUNCTION IF EXISTS "pgxext_notify_public_users_user_changed"();`,
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("SQL missing %q:\n%s", want, sql)
		}
	}
}
