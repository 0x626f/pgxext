// Package repository provides a generic, type-safe query builder for
// PostgreSQL backed by pgxext.DataSource. Struct fields are mapped to
// columns once at repository creation time via reflection on the "DataSource"
// struct tag; all subsequent query building is reflection-free.
package repository

// ── Operator ─────────────────────────────────────────────────────────────────

// Operator represents a SQL comparison operator.
// Its integer value is the index into operatorStrings.
type Operator uint8

const (
	Equals         Operator = iota // =
	NotEquals                      // !=
	Less                           // <
	LessOrEqual                    // <=
	Greeter                        // >
	GreeterOrEqual                 // >=
	Like                           // LIKE
	ILike                          // ILIKE
	In                             // IN
	NotIn                          // NOT IN
	IsNull                         // IS NULL
	IsNotNull                      // IS NOT NULL
	RegexMatch                     // ~   (case-sensitive)
	RegexIMatch                    // ~*  (case-insensitive)
	NotRegexMatch                  // !~  (case-sensitive)
	NotRegexIMatch                 // !~* (case-insensitive)
	SimilarTo                      // SIMILAR TO
	NotSimilarTo                   // NOT SIMILAR TO
)

// operatorStrings maps each Operator constant to its SQL representation.
var operatorStrings = [...]string{
	Equals:         "=",
	NotEquals:      "!=",
	Less:           "<",
	LessOrEqual:    "<=",
	Greeter:        ">",
	GreeterOrEqual: ">=",
	Like:           "LIKE",
	ILike:          "ILIKE",
	In:             "IN",
	NotIn:          "NOT IN",
	IsNull:         "IS NULL",
	IsNotNull:      "IS NOT NULL",
	RegexMatch:     "~",
	RegexIMatch:    "~*",
	NotRegexMatch:  "!~",
	NotRegexIMatch: "!~*",
	SimilarTo:      "SIMILAR TO",
	NotSimilarTo:   "NOT SIMILAR TO",
}

func (op Operator) String() string {
	if int(op) < len(operatorStrings) {
		return operatorStrings[op]
	}
	return "="
}

// ── JoinType ──────────────────────────────────────────────────────────────────

// JoinType represents a SQL JOIN variant.
type JoinType uint8

const (
	Inner JoinType = iota
	Left
	Right
	Full
)

// joinTypeStrings maps each JoinType constant to its SQL keyword.
var joinTypeStrings = [...]string{
	Inner: "INNER",
	Left:  "LEFT",
	Right: "RIGHT",
	Full:  "FULL",
}

func (jt JoinType) String() string {
	if int(jt) < len(joinTypeStrings) {
		return joinTypeStrings[jt]
	}
	return "INNER"
}

// ── SortOption ────────────────────────────────────────────────────────────────

// SortOption represents ASC or DESC sort order.
type SortOption uint8

const (
	DESC SortOption = iota
	ASC
)

// sortOptionStrings maps each SortOption constant to its SQL keyword.
var sortOptionStrings = [...]string{
	DESC: "DESC",
	ASC:  "ASC",
}

func (s SortOption) String() string {
	if int(s) < len(sortOptionStrings) {
		return sortOptionStrings[s]
	}
	return "DESC"
}

// ── Clause types ──────────────────────────────────────────────────────────────

// WhereClause holds a single WHERE condition used in query builders.
// values holds one element for scalar operators and multiple for In/NotIn.
// or indicates this clause is OR-connected to the previous one (AND otherwise).
type WhereClause struct {
	property Property
	op       Operator
	values   []any
	or       bool
}

// JoinClause describes a JOIN to another table.
// on and value are column references (e.g. "users.id", "orders.user_id").
// alias is optional; when set the table is rendered as "table AS alias".
type JoinClause struct {
	kind  JoinType
	table string
	alias string
	on    string
	op    Operator
	value string
}

// SetClause is a single SET assignment in an UPDATE statement.
type SetClause struct {
	property Property
	value    any
}
