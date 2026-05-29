// Package repository provides a generic PostgreSQL query builder.
package repository

// ── Operator ─────────────────────────────────────────────────────────────────

// Operator is a SQL comparison operator.
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

// operatorStrings maps operators to SQL.
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

// JoinType is a SQL JOIN type.
type JoinType uint8

const (
	Inner JoinType = iota
	Left
	Right
	Full
)

// joinTypeStrings maps joins to SQL.
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

// SortOption is ASC or DESC.
type SortOption uint8

const (
	DESC SortOption = iota
	ASC
)

// sortOptionStrings maps sort options to SQL.
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

// WhereClause is a WHERE condition.
type WhereClause struct {
	property Property
	op       Operator
	values   []any
	or       bool
}

// JoinClause is a JOIN clause.
type JoinClause struct {
	kind  JoinType
	table string
	alias string
	on    string
	op    Operator
	value string
}

// SetClause is a SET assignment.
type SetClause struct {
	property Property
	value    any
}

// CTEClause is a common table expression.
type CTEClause struct {
	name      string
	query     string
	recursive bool
}
