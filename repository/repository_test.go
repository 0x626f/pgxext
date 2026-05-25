package repository

import (
	"reflect"
	"testing"
)

// ---------------------------------------------------------------------------
// Test structs
// ---------------------------------------------------------------------------

type simpleStruct struct {
	ID   int    `db:"id"`
	Name string `db:"name"`
	Age  int    `db:"age"`
}

type withOptions struct {
	ID   int    `db:"id,omitempty"`
	Name string `db:"name,omitempty"`
}

type withSkipped struct {
	ID       int    `db:"id"`
	Internal string `db:"-"`
	Name     string `db:"name"`
}

type withNoTag struct {
	ID   int    `db:"id"`
	Name string // no tag → field name used
}

type unexportedFields struct {
	ID       int    `db:"id"`
	internal string `db:"internal"` //nolint:unused // intentionally unexported
}

type embeddedBase struct {
	CreatedAt string `db:"created_at"`
	UpdatedAt string `db:"updated_at"`
}

type withEmbedded struct {
	ID   int    `db:"id"`
	Name string `db:"name"`
	embeddedBase
}

type deepEmbedded struct {
	withEmbedded
	Extra string `db:"extra"`
}

type withEmbeddedPointer struct {
	ID            int `db:"id"`
	*embeddedBase     // embedded pointer — must NOT be recursed into
}

type allSkipped struct {
	A string `db:"-"`
	B string `db:"-"`
}

type empty struct{}

// ---------------------------------------------------------------------------
// NewRepository
// ---------------------------------------------------------------------------

func TestNewRepository_Name(t *testing.T) {
	tbl := NewRepository[simpleStruct](nil, "users")
	if tbl.table != "users" {
		t.Errorf("table = %q, want %q", tbl.table, "users")
	}
}

func TestNewRepository_PropertiesPopulated(t *testing.T) {
	tbl := NewRepository[simpleStruct](nil, "t")
	want := []Property{"id", "name", "age"}
	if !reflect.DeepEqual(tbl.properties, want) {
		t.Errorf("properties = %v, want %v", tbl.properties, want)
	}
}

func TestValidateProperties_QualifiedIdentifierAllowed(t *testing.T) {
	tbl := NewRepository[simpleStruct](nil, "t")
	if err := tbl.validateProperties([]Property{"users.name", "public.users.id"}); err != nil {
		t.Fatalf("validateProperties: %v", err)
	}
}

func TestValidateProperties_InvalidQualifiedIdentifierRejected(t *testing.T) {
	tbl := NewRepository[simpleStruct](nil, "t")
	if err := tbl.validateProperties([]Property{"users.name;DROP"}); err == nil {
		t.Fatal("expected error for invalid qualified property, got nil")
	}
}

// ---------------------------------------------------------------------------
// inspectProperties — tag rules
// ---------------------------------------------------------------------------

func TestInspect_ExplicitDBTag(t *testing.T) {
	got := inspectProperties(reflect.TypeOf(simpleStruct{}))
	want := []Property{"id", "name", "age"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestInspect_TagOptionsStripped(t *testing.T) {
	// db:"col,omitempty" → column name must be "col", not "col,omitempty"
	got := inspectProperties(reflect.TypeOf(withOptions{}))
	want := []Property{"id", "name"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestInspect_DashSkipsField(t *testing.T) {
	got := inspectProperties(reflect.TypeOf(withSkipped{}))
	want := []Property{"id", "name"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestInspect_NoTagUsesFieldName(t *testing.T) {
	got := inspectProperties(reflect.TypeOf(withNoTag{}))
	want := []Property{"id", "Name"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestInspect_UnexportedFieldSkipped(t *testing.T) {
	got := inspectProperties(reflect.TypeOf(unexportedFields{}))
	want := []Property{"id"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// ---------------------------------------------------------------------------
// inspectProperties — embedding rules
// ---------------------------------------------------------------------------

func TestInspect_AnonymousEmbeddedStructRecursed(t *testing.T) {
	got := inspectProperties(reflect.TypeOf(withEmbedded{}))
	// embeddedBase fields follow the outer fields in declaration order
	want := []Property{"id", "name", "created_at", "updated_at"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestInspect_DeepEmbeddingRecursed(t *testing.T) {
	got := inspectProperties(reflect.TypeOf(deepEmbedded{}))
	want := []Property{"id", "name", "created_at", "updated_at", "extra"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestInspect_EmbeddedPointerNotRecursed(t *testing.T) {
	// *embeddedBase is an embedded pointer: pgx does not recurse into it but
	// also does not skip it — it falls through to the tag-lookup path and
	// records the type name ("embeddedBase") as the column name, exactly as
	// pgx's computeNamedStructFields does.
	got := inspectProperties(reflect.TypeOf(withEmbeddedPointer{}))
	want := []Property{"id", "embeddedBase"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// ---------------------------------------------------------------------------
// Edge cases
// ---------------------------------------------------------------------------

func TestInspect_AllSkipped(t *testing.T) {
	got := inspectProperties(reflect.TypeOf(allSkipped{}))
	if len(got) != 0 {
		t.Errorf("expected empty properties, got %v", got)
	}
}

func TestInspect_EmptyStruct(t *testing.T) {
	got := inspectProperties(reflect.TypeOf(empty{}))
	if len(got) != 0 {
		t.Errorf("expected empty properties, got %v", got)
	}
}

func TestInspect_PointerToStruct(t *testing.T) {
	// NewRepository[T] must handle *T transparently.
	tbl := NewRepository[*simpleStruct](nil, "t")
	want := []Property{"id", "name", "age"}
	if !reflect.DeepEqual(tbl.properties, want) {
		t.Errorf("properties = %v, want %v", tbl.properties, want)
	}
}

// ---------------------------------------------------------------------------
// Determinism
// ---------------------------------------------------------------------------

func TestInspect_Deterministic(t *testing.T) {
	// Repeated calls for the same type must return equal results.
	typ := reflect.TypeOf(simpleStruct{})
	first := inspectProperties(typ)
	second := inspectProperties(typ)
	if !reflect.DeepEqual(first, second) {
		t.Errorf("non-deterministic: %v != %v", first, second)
	}
}
