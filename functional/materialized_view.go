package functional

import (
	"context"
	"fmt"
	"strings"

	"github.com/0x626f/pgxext"
)

// MaterializedView builds CREATE MATERIALIZED VIEW statements.
type MaterializedView struct {
	name          string
	query         string
	ifNotExists   bool
	withData      bool
	withDataSet   bool
	storageParams []string
	tablespace    string
}

// NewMaterializedView creates a materialized view builder.
func NewMaterializedView(name string) *MaterializedView {
	return &MaterializedView{name: name, withData: true}
}

// IfNotExists emits CREATE MATERIALIZED VIEW IF NOT EXISTS.
func (v *MaterializedView) IfNotExists() *MaterializedView {
	v.ifNotExists = true
	return v
}

// As sets the SELECT query backing the materialized view.
func (v *MaterializedView) As(query string) *MaterializedView {
	v.query = query
	return v
}

// WithNoData creates the materialized view without populating it.
func (v *MaterializedView) WithNoData() *MaterializedView {
	v.withData = false
	v.withDataSet = true
	return v
}

// WithData creates the materialized view and populates it.
func (v *MaterializedView) WithData() *MaterializedView {
	v.withData = true
	v.withDataSet = true
	return v
}

// WithStorageParameter adds a WITH storage parameter.
func (v *MaterializedView) WithStorageParameter(name, value string) *MaterializedView {
	v.storageParams = append(v.storageParams, name+" = "+value)
	return v
}

// InTablespace adds TABLESPACE.
func (v *MaterializedView) InTablespace(name string) *MaterializedView {
	v.tablespace = name
	return v
}

// BuildSQL builds CREATE MATERIALIZED VIEW SQL.
func (v *MaterializedView) BuildSQL() (string, error) {
	name, err := quoteQualifiedIdentifier(v.name)
	if err != nil {
		return "", err
	}
	if err := validateSQLFragment("materialized view query", v.query); err != nil {
		return "", err
	}

	var sb strings.Builder
	sb.WriteString("CREATE MATERIALIZED VIEW ")
	if v.ifNotExists {
		sb.WriteString("IF NOT EXISTS ")
	}
	sb.WriteString(name)

	if len(v.storageParams) > 0 {
		params := make([]string, len(v.storageParams))
		for i, param := range v.storageParams {
			name, value, ok := strings.Cut(param, " = ")
			if !ok || validateIdentifier(name) != nil || validateDefinitionFragment("storage parameter value", value) != nil {
				return "", fmt.Errorf("database: invalid storage parameter %q", param)
			}
			params[i] = name + " = " + value
		}
		fmt.Fprintf(&sb, "\nWITH (%s)", strings.Join(params, ", "))
	}

	if v.tablespace != "" {
		tablespace, err := quoteIdentifier(v.tablespace)
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&sb, "\nTABLESPACE %s", tablespace)
	}

	fmt.Fprintf(&sb, " AS\n%s", strings.TrimSpace(v.query))
	if v.withDataSet && !v.withData {
		sb.WriteString("\nWITH NO DATA")
	} else {
		sb.WriteString("\nWITH DATA")
	}
	sb.WriteByte(';')
	return sb.String(), nil
}

// DropSQL builds DROP MATERIALIZED VIEW SQL.
func (v *MaterializedView) DropSQL(ifExists bool) (string, error) {
	name, err := quoteQualifiedIdentifier(v.name)
	if err != nil {
		return "", err
	}
	if ifExists {
		return fmt.Sprintf("DROP MATERIALIZED VIEW IF EXISTS %s;", name), nil
	}
	return fmt.Sprintf("DROP MATERIALIZED VIEW %s;", name), nil
}

// RefreshSQL builds REFRESH MATERIALIZED VIEW SQL.
func (v *MaterializedView) RefreshSQL(concurrently bool, withData bool) (string, error) {
	name, err := quoteQualifiedIdentifier(v.name)
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	sb.WriteString("REFRESH MATERIALIZED VIEW ")
	if concurrently {
		sb.WriteString("CONCURRENTLY ")
	}
	sb.WriteString(name)
	if withData {
		sb.WriteString(" WITH DATA")
	} else {
		sb.WriteString(" WITH NO DATA")
	}
	sb.WriteByte(';')
	return sb.String(), nil
}

// Apply creates the materialized view.
func (v *MaterializedView) Apply(ctx context.Context, ds *pgxext.DataSource) error {
	sql, err := v.BuildSQL()
	if err != nil {
		return err
	}
	_, err = ds.Exec(ctx, sql)
	return err
}

// Drop drops the materialized view.
func (v *MaterializedView) Drop(ctx context.Context, ds *pgxext.DataSource, ifExists bool) error {
	sql, err := v.DropSQL(ifExists)
	if err != nil {
		return err
	}
	_, err = ds.Exec(ctx, sql)
	return err
}

// Refresh refreshes the materialized view.
func (v *MaterializedView) Refresh(ctx context.Context, ds *pgxext.DataSource, concurrently bool, withData bool) error {
	sql, err := v.RefreshSQL(concurrently, withData)
	if err != nil {
		return err
	}
	_, err = ds.Exec(ctx, sql)
	return err
}
