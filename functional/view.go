package functional

import (
	"context"
	"fmt"
	"strings"

	"github.com/0x626f/pgxext"
)

// View builds CREATE VIEW statements.
type View struct {
	name        string
	query       string
	orReplace   bool
	checkOption CheckOption
}

// NewView creates a view builder.
func NewView(name string) *View {
	return &View{name: name}
}

// As sets the SELECT query backing the view.
func (v *View) As(query string) *View {
	v.query = query
	return v
}

// OrReplace emits CREATE OR REPLACE VIEW.
func (v *View) OrReplace() *View {
	v.orReplace = true
	return v
}

// WithCheckOption adds WITH [LOCAL|CASCADED] CHECK OPTION.
func (v *View) WithCheckOption(option CheckOption) *View {
	v.checkOption = option
	return v
}

// BuildSQL builds CREATE VIEW SQL.
func (v *View) BuildSQL() (string, error) {
	name, err := quoteQualifiedIdentifier(v.name)
	if err != nil {
		return "", err
	}
	if err := validateSQLFragment("view query", v.query); err != nil {
		return "", err
	}

	var sb strings.Builder
	sb.WriteString("CREATE ")
	if v.orReplace {
		sb.WriteString("OR REPLACE ")
	}
	fmt.Fprintf(&sb, "VIEW %s AS\n%s", name, strings.TrimSpace(v.query))

	if v.checkOption != "" {
		switch v.checkOption {
		case LocalCheckOption, CascadedCheckOption:
			fmt.Fprintf(&sb, "\nWITH %s CHECK OPTION", v.checkOption)
		default:
			return "", fmt.Errorf("database: unsupported check option %q", v.checkOption)
		}
	}
	sb.WriteByte(';')
	return sb.String(), nil
}

// DropSQL builds DROP VIEW SQL.
func (v *View) DropSQL(ifExists bool) (string, error) {
	name, err := quoteQualifiedIdentifier(v.name)
	if err != nil {
		return "", err
	}
	if ifExists {
		return fmt.Sprintf("DROP VIEW IF EXISTS %s;", name), nil
	}
	return fmt.Sprintf("DROP VIEW %s;", name), nil
}

// Apply creates or replaces the view.
func (v *View) Apply(ctx context.Context, ds *pgxext.DataSource) error {
	sql, err := v.BuildSQL()
	if err != nil {
		return err
	}
	_, err = ds.Exec(ctx, sql)
	return err
}

// Drop drops the view.
func (v *View) Drop(ctx context.Context, ds *pgxext.DataSource, ifExists bool) error {
	sql, err := v.DropSQL(ifExists)
	if err != nil {
		return err
	}
	_, err = ds.Exec(ctx, sql)
	return err
}
