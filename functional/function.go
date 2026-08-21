package functional

import (
	"context"
	"fmt"
	"strings"

	"github.com/0x626f/pgxext"
)

// Function builds CREATE FUNCTION statements.
type Function struct {
	name            string
	arguments       []string
	returns         string
	language        string
	body            string
	orReplace       bool
	volatility      Volatility
	securityDefiner bool
	strict          bool
}

// NewFunction creates a function builder.
func NewFunction(name string) *Function {
	return &Function{name: name, language: "plpgsql"}
}

// OrReplace emits CREATE OR REPLACE FUNCTION.
func (f *Function) OrReplace() *Function {
	f.orReplace = true
	return f
}

// WithArguments sets the function arguments.
func (f *Function) WithArguments(args ...string) *Function {
	f.arguments = append([]string(nil), args...)
	return f
}

// Returns sets the function return type.
func (f *Function) Returns(returnType string) *Function {
	f.returns = returnType
	return f
}

// Language sets the function language.
func (f *Function) Language(language string) *Function {
	f.language = language
	return f
}

// Body sets the function body.
func (f *Function) Body(body string) *Function {
	f.body = body
	return f
}

// WithVolatility sets VOLATILE, STABLE, or IMMUTABLE.
func (f *Function) WithVolatility(volatility Volatility) *Function {
	f.volatility = volatility
	return f
}

// SecurityDefiner emits SECURITY DEFINER.
func (f *Function) SecurityDefiner() *Function {
	f.securityDefiner = true
	return f
}

// Strict emits STRICT.
func (f *Function) Strict() *Function {
	f.strict = true
	return f
}

// BuildSQL builds CREATE FUNCTION SQL.
func (f *Function) BuildSQL() (string, error) {
	name, err := quoteQualifiedIdentifier(f.name)
	if err != nil {
		return "", err
	}
	if err := f.validate(); err != nil {
		return "", err
	}

	var sb strings.Builder
	sb.WriteString("CREATE ")
	if f.orReplace {
		sb.WriteString("OR REPLACE ")
	}
	fmt.Fprintf(&sb, "FUNCTION %s(%s)\nRETURNS %s\nLANGUAGE %s",
		name,
		strings.Join(f.arguments, ", "),
		strings.TrimSpace(f.returns),
		f.language,
	)
	if f.volatility != "" {
		fmt.Fprintf(&sb, "\n%s", f.volatility)
	}
	if f.securityDefiner {
		sb.WriteString("\nSECURITY DEFINER")
	}
	if f.strict {
		sb.WriteString("\nSTRICT")
	}
	body := strings.TrimSpace(f.body)
	quote := dollarQuote(body)
	fmt.Fprintf(&sb, "\nAS %s\n%s\n%s;", quote, body, quote)
	return sb.String(), nil
}

// DropSQL builds DROP FUNCTION SQL.
func (f *Function) DropSQL(ifExists bool) (string, error) {
	name, err := quoteQualifiedIdentifier(f.name)
	if err != nil {
		return "", err
	}
	for _, arg := range f.arguments {
		if err := validateDefinitionFragment("function argument", arg); err != nil {
			return "", err
		}
	}
	if ifExists {
		return fmt.Sprintf("DROP FUNCTION IF EXISTS %s(%s);", name, strings.Join(f.arguments, ", ")), nil
	}
	return fmt.Sprintf("DROP FUNCTION %s(%s);", name, strings.Join(f.arguments, ", ")), nil
}

// Apply creates or replaces the function.
func (f *Function) Apply(ctx context.Context, ds *pgxext.DataSource) error {
	sql, err := f.BuildSQL()
	if err != nil {
		return err
	}
	_, err = ds.Exec(ctx, sql)
	return err
}

// Drop drops the function.
func (f *Function) Drop(ctx context.Context, ds *pgxext.DataSource, ifExists bool) error {
	sql, err := f.DropSQL(ifExists)
	if err != nil {
		return err
	}
	_, err = ds.Exec(ctx, sql)
	return err
}

func (f *Function) validate() error {
	if err := validateDefinitionFragment("function return type", f.returns); err != nil {
		return err
	}
	if err := validateSQLFragment("function body", f.body); err != nil {
		return err
	}
	if err := validateIdentifier(f.language); err != nil {
		return err
	}
	for _, arg := range f.arguments {
		if err := validateDefinitionFragment("function argument", arg); err != nil {
			return err
		}
	}
	switch f.volatility {
	case "", Volatile, Stable, Immutable:
		return nil
	default:
		return fmt.Errorf("database: unsupported volatility %q", f.volatility)
	}
}

func dollarQuote(body string) string {
	quote := "$$"
	for i := 0; strings.Contains(body, quote); i++ {
		quote = fmt.Sprintf("$pgxext%d$", i)
	}
	return quote
}
