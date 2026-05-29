package repository

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/0x626f/pgxext"
)

// Property is a column name.
type Property = string

// Repository builds queries for a table.
type Repository[T any] struct {
	DataSource  *pgxext.DataSource
	table       string
	properties  []Property
	propertySet map[string]struct{}
}

// NewRepository creates a Repository.
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

// Select starts a SELECT query.
func (repository *Repository[T]) Select() *SelectQuery[T] {
	return &SelectQuery[T]{repo: repository}
}

// Insert starts an INSERT query.
func (repository *Repository[T]) Insert() *InsertQuery[T] {
	return &InsertQuery[T]{repo: repository}
}

// Update starts an UPDATE query.
func (repository *Repository[T]) Update() *UpdateQuery[T] {
	return &UpdateQuery[T]{repo: repository}
}

// Delete starts a DELETE query.
func (repository *Repository[T]) Delete() *DeleteQuery[T] {
	return &DeleteQuery[T]{repo: repository}
}

// validateProperties validates mapped columns.
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

// collectProperties returns mapped column names.
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
