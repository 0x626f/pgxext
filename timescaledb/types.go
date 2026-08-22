package timescaledb

import (
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Relation is a PostgreSQL relation name. Schema may be empty to use the
// connection's search_path. Schema and Name are always quoted as identifiers.
type Relation struct {
	Schema string
	Name   string
}

// Column is a PostgreSQL column identifier.
type Column string

// Alias is a quoted SELECT-list alias.
type Alias string

// TrustedExpression is SQL supplied by trusted, static application code.
// Never construct a TrustedExpression from request input or other untrusted
// data. Runtime values belong in typed predicates and query parameters.
type TrustedExpression string

var (
	// ErrExtensionNotInstalled means the timescaledb extension is not installed
	// in the current database.
	ErrExtensionNotInstalled = errors.New("timescaledb extension is not installed")
	// ErrUnsupportedVersion means the installed TimescaleDB version is below the
	// package compatibility baseline or has an unsupported major version.
	ErrUnsupportedVersion = errors.New("unsupported TimescaleDB version")
	// ErrCapabilityUnavailable means an installed edition or version does not
	// provide a requested capability.
	ErrCapabilityUnavailable = errors.New("TimescaleDB capability is unavailable")
	// ErrNotHypertable means a relation exists but is not a Timescale hypertable.
	ErrNotHypertable = errors.New("relation is not a hypertable")
	// ErrNotContinuousAggregate means a relation is not a continuous aggregate.
	ErrNotContinuousAggregate = errors.New("relation is not a continuous aggregate")
	// ErrPolicyNotFound means the requested Timescale policy does not exist.
	ErrPolicyNotFound = errors.New("TimescaleDB policy not found")
	// ErrPolicyDrift means Ensure found a policy whose configuration differs.
	ErrPolicyDrift = errors.New("TimescaleDB policy configuration drift")
)

// CapabilityError identifies a feature rejected by the installed TimescaleDB
// version. It unwraps to ErrCapabilityUnavailable and, when applicable,
// ErrUnsupportedVersion.
type CapabilityError struct {
	Feature  string
	Version  string
	Required string

	unsupportedVersion bool
}

func (e *CapabilityError) Error() string {
	if e.Version == "" {
		return fmt.Sprintf("timescaledb: %s requires TimescaleDB %s", e.Feature, e.Required)
	}
	return fmt.Sprintf("timescaledb: %s requires TimescaleDB %s (installed %s)", e.Feature, e.Required, e.Version)
}

func (e *CapabilityError) Unwrap() error {
	if e.unsupportedVersion {
		return errors.Join(ErrCapabilityUnavailable, ErrUnsupportedVersion)
	}
	return ErrCapabilityUnavailable
}

// PolicyAction reports what an ensure or reconcile operation did.
type PolicyAction string

const (
	PolicyCreated   PolicyAction = "created"
	PolicyUnchanged PolicyAction = "unchanged"
	PolicyReplaced  PolicyAction = "replaced"
)

// PolicyDecision is the pure decision made before changing a policy.
type PolicyDecision string

const (
	PolicyDecisionCreate   PolicyDecision = "create"
	PolicyDecisionNoop     PolicyDecision = "noop"
	PolicyDecisionReplace  PolicyDecision = "replace"
	PolicyDecisionConflict PolicyDecision = "conflict"
)

func (r Relation) validate() error {
	if err := validateIdentifier("relation", r.Name); err != nil {
		return err
	}
	if r.Schema != "" {
		if err := validateIdentifier("schema", r.Schema); err != nil {
			return err
		}
	}
	return nil
}

func (r Relation) quoted() (string, error) {
	if err := r.validate(); err != nil {
		return "", err
	}
	if r.Schema == "" {
		return pgx.Identifier{r.Name}.Sanitize(), nil
	}
	return pgx.Identifier{r.Schema, r.Name}.Sanitize(), nil
}

// String returns a safely quoted, schema-qualified SQL identifier. Invalid
// zero values return an empty string; builders return the corresponding error.
func (r Relation) String() string {
	quoted, _ := r.quoted()
	return quoted
}

func (r Relation) regclassText() (string, error) {
	return r.quoted()
}

func (c Column) validate() error {
	return validateIdentifier("column", string(c))
}

func (c Column) quoted() (string, error) {
	if err := c.validate(); err != nil {
		return "", err
	}
	return pgx.Identifier{string(c)}.Sanitize(), nil
}

func (a Alias) validate() error {
	return validateIdentifier("alias", string(a))
}

func (a Alias) quoted() (string, error) {
	if err := a.validate(); err != nil {
		return "", err
	}
	return pgx.Identifier{string(a)}.Sanitize(), nil
}

func validateIdentifier(kind, value string) error {
	if value == "" {
		return fmt.Errorf("timescaledb: empty %s identifier", kind)
	}
	if strings.ContainsRune(value, 0) {
		return fmt.Errorf("timescaledb: %s identifier contains NUL", kind)
	}
	return nil
}

func validateTrusted(kind string, expression TrustedExpression) error {
	if strings.TrimSpace(string(expression)) == "" {
		return fmt.Errorf("timescaledb: empty trusted %s", kind)
	}
	if strings.ContainsRune(string(expression), 0) {
		return fmt.Errorf("timescaledb: trusted %s contains NUL", kind)
	}
	return nil
}

func sqlLiteral(value string) string {
	value = strings.ReplaceAll(value, "'", "''")
	if strings.Contains(value, `\`) {
		value = strings.ReplaceAll(value, `\`, `\\`)
		return "E'" + value + "'"
	}
	return "'" + value + "'"
}

func boolSQL(value bool) string {
	if value {
		return "TRUE"
	}
	return "FALSE"
}

func relationContext(relation Relation) string {
	if relation.Schema == "" {
		return fmt.Sprintf("%q", relation.Name)
	}
	return fmt.Sprintf("%q.%q", relation.Schema, relation.Name)
}

func closeRows(rows pgx.Rows) {
	if rows != nil {
		rows.Close()
	}
}
