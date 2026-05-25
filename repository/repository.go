package repository

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/0x626f/pgxext"
)

// Property is a type alias for a database column name.
type Property = string

// Repository holds the table name, the ordered list of mapped column names for
// struct type T (derived from T's DataSource struct tags), and the DataSource used
// by all query builders produced from this repository.
type Repository[T any] struct {
	DataSource  *pgxext.DataSource
	table       string
	properties  []Property
	propertySet map[string]struct{}
}

// NewRepository creates a Repository bound to DataSource and the given table name.
// Type T is introspected once to populate properties following pgx's DataSource-tag rules:
//   - Exported fields with no DataSource tag are included using the Go field name.
//   - db:"col"        → column name is "col" (options after comma are stripped).
//   - db:"-"          → field is skipped.
//   - Anonymous embedded structs are recursed into (embedded pointers are not).
//   - Unexported non-anonymous fields are skipped.
func NewRepository[T any](source *pgxext.DataSource, table string) *Repository[T] {
	var zero T
	t := reflect.TypeOf(zero)
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	props := inspectProperties(t)
	set := make(map[string]struct{}, len(props))
	for _, p := range props {
		set[p] = struct{}{}
	}
	return &Repository[T]{
		DataSource:  source,
		table:       table,
		properties:  props,
		propertySet: set,
	}
}

// Select returns a SelectQuery that will SELECT all mapped columns of T.
func (repository *Repository[T]) Select() *SelectQuery[T] {
	return &SelectQuery[T]{repo: repository}
}

// Insert returns an InsertQuery. Use .Set() to specify column values.
func (repository *Repository[T]) Insert() *InsertQuery[T] {
	return &InsertQuery[T]{repo: repository}
}

// Update returns an UpdateQuery. Use .Set() to specify column assignments.
func (repository *Repository[T]) Update() *UpdateQuery[T] {
	return &UpdateQuery[T]{repo: repository}
}

// Delete returns a DeleteQuery.
func (repository *Repository[T]) Delete() *DeleteQuery[T] {
	return &DeleteQuery[T]{repo: repository}
}

// validateProperties returns an error if any unqualified property is not a
// mapped column of T. Qualified names (containing ".") are allowed freely so
// that JOIN columns from other tables can be used in WHERE clauses.
func (repository *Repository[T]) validateProperties(props []Property) error {
	for _, p := range props {
		if strings.Contains(p, ".") {
			if !isQualifiedProperty(p) {
				return fmt.Errorf("%s repository: invalid qualified property %q", repository.table, p)
			}
			continue
		}
		if _, ok := repository.propertySet[p]; !ok {
			return fmt.Errorf("%s repository: unknown property %q", repository.table, p)
		}
	}
	return nil
}

func isQualifiedProperty(p Property) bool {
	parts := strings.Split(p, ".")
	for _, part := range parts {
		if !isIdentifier(part) {
			return false
		}
	}
	return true
}

func isIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '_' {
			continue
		}
		if i > 0 && r >= '0' && r <= '9' {
			continue
		}
		return false
	}
	return true
}

// ── introspection ─────────────────────────────────────────────────────────────

func inspectProperties(t reflect.Type) []Property {
	return collectProperties(t, nil)
}

// collectProperties mirrors pgx's computeNamedStructFields traversal, but
// produces only the ordered list of column names.
func collectProperties(t reflect.Type, props []Property) []Property {
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)

		// Skip unexported non-anonymous fields.
		if sf.PkgPath != "" && !sf.Anonymous {
			continue
		}

		// Recurse into anonymous embedded structs, but not embedded pointers.
		if sf.Anonymous && sf.Type.Kind() == reflect.Struct {
			props = collectProperties(sf.Type, props)
			continue
		}

		dbTag, tagPresent := sf.Tag.Lookup("db")
		if tagPresent {
			// Strip options: DataSource:"col,omitempty" → "col"
			dbTag, _, _ = strings.Cut(dbTag, ",")
		}

		// Explicitly excluded field.
		if dbTag == "-" {
			continue
		}

		col := dbTag
		if !tagPresent || col == "" {
			col = sf.Name
		}

		props = append(props, col)
	}
	return props
}
