package repository

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// ── SelectQuery ───────────────────────────────────────────────────────────────

// SelectQuery builds a SELECT statement for Repository[T].
type SelectQuery[T any] struct {
	repo    *Repository[T]
	joins   []JoinClause
	wheres  []WhereClause
	orderBy Property
	sortOpt SortOption
	limit   uint
}

// Where adds an AND condition. Qualified names (containing ".") bypass property
// validation so JOIN columns from other tables can be filtered on.
func (query *SelectQuery[T]) Where(prop Property, op Operator, values ...any) *SelectQuery[T] {
	query.wheres = append(query.wheres, WhereClause{property: prop, op: op, values: values})
	return query
}

// OrWhere adds an OR condition. Consecutive OrWhere calls (and the preceding
// Where call) are grouped in parentheses: (a OR b OR c).
func (query *SelectQuery[T]) OrWhere(prop Property, op Operator, values ...any) *SelectQuery[T] {
	query.wheres = append(query.wheres, WhereClause{property: prop, op: op, values: values, or: true})
	return query
}

// Join adds an INNER JOIN clause. on and value are column references
// (e.g. "users.id", "orders.user_id"). An optional alias renders as
// "table AS alias", which is required when joining the same table more than once.
func (query *SelectQuery[T]) Join(table, on string, op Operator, value string, alias ...string) *SelectQuery[T] {
	return query.appendJoin(Inner, table, on, op, value, alias...)
}

// LeftJoin adds a LEFT JOIN clause. See Join for alias semantics.
func (query *SelectQuery[T]) LeftJoin(table, on string, op Operator, value string, alias ...string) *SelectQuery[T] {
	return query.appendJoin(Left, table, on, op, value, alias...)
}

// RightJoin adds a RIGHT JOIN clause. See Join for alias semantics.
func (query *SelectQuery[T]) RightJoin(table, on string, op Operator, value string, alias ...string) *SelectQuery[T] {
	return query.appendJoin(Right, table, on, op, value, alias...)
}

// FullJoin adds a FULL JOIN clause. See Join for alias semantics.
func (query *SelectQuery[T]) FullJoin(table, on string, op Operator, value string, alias ...string) *SelectQuery[T] {
	return query.appendJoin(Full, table, on, op, value, alias...)
}

func (query *SelectQuery[T]) appendJoin(kind JoinType, table, on string, op Operator, value string, alias ...string) *SelectQuery[T] {
	j := JoinClause{kind: kind, table: table, on: on, op: op, value: value}
	if len(alias) > 0 {
		j.alias = alias[0]
	}
	query.joins = append(query.joins, j)
	return query
}

// OrderBy sets the ORDER BY clause.
func (query *SelectQuery[T]) OrderBy(prop Property, sort SortOption) *SelectQuery[T] {
	query.orderBy = prop
	query.sortOpt = sort
	return query
}

// Limit sets the LIMIT clause.
func (query *SelectQuery[T]) Limit(n uint) *SelectQuery[T] {
	query.limit = n
	return query
}

// buildFrom writes "FROM table [JOIN …] [WHERE …]" into sb and appends
// parameterised WHERE values to args. It is shared by Execute and Count.
func (query *SelectQuery[T]) buildFrom(sb *strings.Builder, args *[]any) {
	fmt.Fprintf(sb, " FROM %s", query.repo.table)

	for _, j := range query.joins {
		tableRef := j.table
		if j.alias != "" {
			tableRef = j.table + " AS " + j.alias
		}
		fmt.Fprintf(sb, " %s JOIN %s ON %s %s %s", j.kind, tableRef, j.on, j.op, j.value)
	}

	if clause := buildWhereSQL(query.wheres, args); clause != "" {
		fmt.Fprintf(sb, " %s", clause)
	}
}

// Execute builds and runs the SELECT query, always selecting all mapped columns.
// Returns all matching rows scanned into []*T via pgx's DataSource-tag mapping.
func (query *SelectQuery[T]) Execute(ctx context.Context) ([]*T, error) {
	var args []any
	var sb strings.Builder

	cols := query.repo.properties
	if len(query.joins) > 0 {
		qualified := make([]string, len(cols))
		for i, c := range cols {
			qualified[i] = query.repo.table + "." + c
		}
		cols = qualified
	}
	fmt.Fprintf(&sb, "SELECT %s", strings.Join(cols, ", "))
	query.buildFrom(&sb, &args)

	if query.orderBy != "" {
		fmt.Fprintf(&sb, " ORDER BY %s %s", query.orderBy, query.sortOpt)
	}
	if query.limit > 0 {
		fmt.Fprintf(&sb, " LIMIT %d", query.limit)
	}

	rows, err := query.repo.DataSource.Query(ctx, sb.String(), args...)
	if err != nil {
		return nil, err
	}
	return pgx.CollectRows(rows, pgx.RowToAddrOfStructByName[T])
}

// Count builds and runs a SELECT COUNT(*) query with the same joins and WHERE
// clauses as Execute. ORDER BY and LIMIT are ignored.
func (query *SelectQuery[T]) Count(ctx context.Context) (int64, error) {
	var args []any
	var sb strings.Builder

	sb.WriteString("SELECT COUNT(*)")
	query.buildFrom(&sb, &args)

	row, err := query.repo.DataSource.QueryRow(ctx, sb.String(), args...)
	if err != nil {
		return 0, err
	}
	var count int64
	if err := row.Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

// ── InsertQuery ───────────────────────────────────────────────────────────────

// InsertQuery builds an INSERT statement for Repository[T].
type InsertQuery[T any] struct {
	repo             *Repository[T]
	sets             []SetClause
	conflictCols     []Property
	updateOnConflict []Property
}

// Set adds a column value for the INSERT. p must be a mapped property of T.
func (query *InsertQuery[T]) Set(prop Property, value any) *InsertQuery[T] {
	query.sets = append(query.sets, SetClause{property: prop, value: value})
	return query
}

// UpdateOnConflict adds an ON CONFLICT (...) DO UPDATE SET clause.
// conflictCols identifies the unique constraint columns that trigger the
// conflict; updateCols lists which columns to overwrite with their EXCLUDED
// value. All columns must be mapped properties of T.
func (query *InsertQuery[T]) UpdateOnConflict(conflictCols []Property, updateCols ...Property) *InsertQuery[T] {
	query.conflictCols = conflictCols
	query.updateOnConflict = updateCols
	return query
}

// Execute validates, builds, and runs the INSERT query.
// Returns the number of rows affected.
func (query *InsertQuery[T]) Execute(ctx context.Context) (int64, error) {
	if len(query.sets) == 0 {
		return 0, fmt.Errorf("repository: no columns to insert for table %q", query.repo.table)
	}

	setProps := make([]Property, len(query.sets))
	for i, s := range query.sets {
		setProps[i] = s.property
	}
	if err := query.repo.validateProperties(setProps); err != nil {
		return 0, err
	}
	if err := query.repo.validateProperties(query.conflictCols); err != nil {
		return 0, err
	}
	if err := query.repo.validateProperties(query.updateOnConflict); err != nil {
		return 0, err
	}

	cols := make([]string, len(query.sets))
	placeholders := make([]string, len(query.sets))
	args := make([]any, len(query.sets))
	for i, s := range query.sets {
		cols[i] = s.property
		args[i] = s.value
		placeholders[i] = fmt.Sprintf("$%d", i+1)
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "INSERT INTO %s (%s) VALUES (%s)",
		query.repo.table,
		strings.Join(cols, ", "),
		strings.Join(placeholders, ", "),
	)

	if len(query.conflictCols) > 0 && len(query.updateOnConflict) > 0 {
		setClauses := make([]string, len(query.updateOnConflict))
		for i, col := range query.updateOnConflict {
			setClauses[i] = fmt.Sprintf("%s = EXCLUDED.%s", col, col)
		}
		fmt.Fprintf(&sb, " ON CONFLICT (%s) DO UPDATE SET %s",
			strings.Join(query.conflictCols, ", "),
			strings.Join(setClauses, ", "),
		)
	}

	tag, err := query.repo.DataSource.Exec(ctx, sb.String(), args...)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ── UpdateQuery ───────────────────────────────────────────────────────────────

// UpdateQuery builds an UPDATE statement for Repository[T].
type UpdateQuery[T any] struct {
	repo   *Repository[T]
	sets   []SetClause
	wheres []WhereClause
}

// Set adds a SET p = v assignment. p must be a mapped property of T.
func (query *UpdateQuery[T]) Set(prop Property, value any) *UpdateQuery[T] {
	query.sets = append(query.sets, SetClause{property: prop, value: value})
	return query
}

// Where adds an AND condition.
func (query *UpdateQuery[T]) Where(prop Property, op Operator, values ...any) *UpdateQuery[T] {
	query.wheres = append(query.wheres, WhereClause{property: prop, op: op, values: values})
	return query
}

// OrWhere adds an OR condition. See SelectQuery.OrWhere for grouping semantics.
func (query *UpdateQuery[T]) OrWhere(prop Property, op Operator, values ...any) *UpdateQuery[T] {
	query.wheres = append(query.wheres, WhereClause{property: prop, op: op, values: values, or: true})
	return query
}

// Execute validates, builds, and runs the UPDATE query.
// Returns the number of rows affected.
func (query *UpdateQuery[T]) Execute(ctx context.Context) (int64, error) {
	if len(query.sets) == 0 {
		return 0, fmt.Errorf("repository: no SET clauses for UPDATE on table %query", query.repo.table)
	}

	setProps := make([]Property, len(query.sets))
	for i, s := range query.sets {
		setProps[i] = s.property
	}
	if err := query.repo.validateProperties(setProps); err != nil {
		return 0, err
	}

	var args []any
	setClauses := make([]string, 0, len(query.sets))
	for _, s := range query.sets {
		args = append(args, s.value)
		setClauses = append(setClauses, fmt.Sprintf("%s = $%d", s.property, len(args)))
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "UPDATE %s SET %s", query.repo.table, strings.Join(setClauses, ", "))

	if clause := buildWhereSQL(query.wheres, &args); clause != "" {
		fmt.Fprintf(&sb, " %s", clause)
	}

	tag, err := query.repo.DataSource.Exec(ctx, sb.String(), args...)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ── DeleteQuery ───────────────────────────────────────────────────────────────

// DeleteQuery builds a DELETE statement for Repository[T].
type DeleteQuery[T any] struct {
	repo   *Repository[T]
	wheres []WhereClause
}

// Where adds an AND condition.
func (q *DeleteQuery[T]) Where(prop Property, op Operator, values ...any) *DeleteQuery[T] {
	q.wheres = append(q.wheres, WhereClause{property: prop, op: op, values: values})
	return q
}

// OrWhere adds an OR condition. See SelectQuery.OrWhere for grouping semantics.
func (q *DeleteQuery[T]) OrWhere(prop Property, op Operator, values ...any) *DeleteQuery[T] {
	q.wheres = append(q.wheres, WhereClause{property: prop, op: op, values: values, or: true})
	return q
}

// Execute validates, builds, and runs the DELETE query.
// Returns the number of rows affected.
func (q *DeleteQuery[T]) Execute(ctx context.Context) (int64, error) {
	var args []any
	var sb strings.Builder
	fmt.Fprintf(&sb, "DELETE FROM %s", q.repo.table)

	if clause := buildWhereSQL(q.wheres, &args); clause != "" {
		fmt.Fprintf(&sb, " %s", clause)
	}

	tag, err := q.repo.DataSource.Exec(ctx, sb.String(), args...)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// ── SQL helpers ───────────────────────────────────────────────────────────────

// buildWhereSQL renders all clauses and returns the full "WHERE ..." string
// (empty string if no clauses). Consecutive OR-flagged clauses are grouped in
// parentheses and joined with OR; groups are then joined with AND.
//
// Example: Where(a).OrWhere(b).OrWhere(c).Where(d) →
//
//	WHERE (a OR b OR c) AND d
func buildWhereSQL(wheres []WhereClause, args *[]any) string {
	if len(wheres) == 0 {
		return ""
	}

	// Split into AND-separated groups; each group holds one or more clauses
	// joined by OR (a new group starts whenever or==false).
	type group []WhereClause
	var groups []group
	for _, w := range wheres {
		if !w.or || len(groups) == 0 {
			groups = append(groups, group{w})
		} else {
			groups[len(groups)-1] = append(groups[len(groups)-1], w)
		}
	}

	andParts := make([]string, len(groups))
	for i, g := range groups {
		if len(g) == 1 {
			andParts[i] = whereClauseSQL(g[0], args)
		} else {
			orParts := make([]string, len(g))
			for j, w := range g {
				orParts[j] = whereClauseSQL(w, args)
			}
			andParts[i] = "(" + strings.Join(orParts, " OR ") + ")"
		}
	}
	return "WHERE " + strings.Join(andParts, " AND ")
}

// whereClauseSQL renders a single WhereClause, appending parameterised values
// to args and using $n placeholders.
func whereClauseSQL(w WhereClause, args *[]any) string {
	switch w.op {
	case IsNull, IsNotNull:
		return w.property + " " + w.op.String()

	case In, NotIn:
		if len(w.values) == 0 {
			return fmt.Sprintf("%s = $%d", w.property, len(*args)+1)
		}
		ph := make([]string, len(w.values))
		for i, v := range w.values {
			*args = append(*args, v)
			ph[i] = fmt.Sprintf("$%d", len(*args))
		}
		return fmt.Sprintf("%s %s (%s)", w.property, w.op, strings.Join(ph, ", "))

	default:
		*args = append(*args, w.values[0])
		return fmt.Sprintf("%s %s $%d", w.property, w.op, len(*args))
	}
}
