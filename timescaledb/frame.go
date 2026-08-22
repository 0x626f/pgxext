package timescaledb

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/0x626f/pgxext"
	"github.com/jackc/pgx/v5"
)

// FrameQuery builds a typed, bounded time_bucket or time_bucket_gapfill query.
type FrameQuery[T any] struct {
	source     Relation
	timeColumn Column
	bucket     bucketConfig
	dimensions []Column
	measures   []Measure
	predicates []Predicate
	start      time.Time
	end        time.Time
	rangeSet   bool
	gapfill    bool
	ascending  bool
}

// NewFrameQuery creates a frame query. Bucket and Between are required before
// Build or Execute.
func NewFrameQuery[T any](source Relation, timeColumn Column) *FrameQuery[T] {
	return &FrameQuery[T]{source: source, timeColumn: timeColumn, ascending: true}
}

func (q *FrameQuery[T]) Bucket(width Interval) *FrameQuery[T] {
	q.bucket.width = width
	return q
}

func (q *FrameQuery[T]) Dimension(column Column) *FrameQuery[T] {
	q.dimensions = append(q.dimensions, column)
	return q
}

func (q *FrameQuery[T]) Measure(measures ...Measure) *FrameQuery[T] {
	q.measures = append(q.measures, measures...)
	return q
}

// Between sets the required half-open time range [start,end).
func (q *FrameQuery[T]) Between(start, end time.Time) *FrameQuery[T] {
	q.start = start
	q.end = end
	q.rangeSet = true
	return q
}

func (q *FrameQuery[T]) Where(predicates ...Predicate) *FrameQuery[T] {
	q.predicates = append(q.predicates, predicates...)
	return q
}

// GapFill emits time_bucket_gapfill. Missing measures remain NULL unless that
// measure explicitly selects LOCF or interpolation.
func (q *FrameQuery[T]) GapFill() *FrameQuery[T] {
	q.gapfill = true
	return q
}

func (q *FrameQuery[T]) Timezone(timezone string) *FrameQuery[T] {
	q.bucket.timezone = timezone
	return q
}

func (q *FrameQuery[T]) Origin(origin time.Time) *FrameQuery[T] {
	value := origin
	q.bucket.origin = &value
	return q
}

// Offset sets a fixed, non-zero bucket offset. FixedInterval may contain a
// negative duration here even though negative widths are invalid.
func (q *FrameQuery[T]) Offset(offset Interval) *FrameQuery[T] {
	q.bucket.offset = offset
	q.bucket.offsetSet = true
	return q
}

func (q *FrameQuery[T]) OrderAscending() *FrameQuery[T] {
	q.ascending = true
	return q
}

func (q *FrameQuery[T]) OrderDescending() *FrameQuery[T] {
	q.ascending = false
	return q
}

// Build validates the query and returns deterministic SQL and parameter args.
func (q *FrameQuery[T]) Build() (string, []any, error) {
	gapfill := q.gapfill
	for _, measure := range q.measures {
		if measure.fill != fillNull {
			gapfill = true
		}
	}
	if err := q.validate(gapfill); err != nil {
		return "", nil, err
	}
	relation, _ := q.source.quoted()
	timeColumn, _ := q.timeColumn.quoted()
	args := make([]any, 0, 3+len(q.predicates))
	bucket, err := q.bucket.parameterizedSQL(timeColumn, gapfill, &args)
	if err != nil {
		return "", nil, err
	}
	selects := []string{bucket + ` AS "bucket"`}
	for _, dimension := range q.dimensions {
		quoted, _ := dimension.quoted()
		selects = append(selects, quoted)
	}
	for _, measure := range q.measures {
		value, err := measure.selectSQL(gapfill)
		if err != nil {
			return "", nil, err
		}
		selects = append(selects, value)
	}
	args = append(args, q.start, q.end)
	wheres := []string{
		fmt.Sprintf("%s >= $%d::timestamptz", timeColumn, len(args)-1),
		fmt.Sprintf("%s < $%d::timestamptz", timeColumn, len(args)),
	}
	for _, predicate := range q.predicates {
		clause, err := predicate.sql(&args)
		if err != nil {
			return "", nil, err
		}
		wheres = append(wheres, clause)
	}
	groups := make([]string, 1+len(q.dimensions))
	orders := make([]string, len(groups))
	direction := "ASC"
	if !q.ascending {
		direction = "DESC"
	}
	for index := range groups {
		groups[index] = fmt.Sprintf("%d", index+1)
		orders[index] = fmt.Sprintf("%d %s", index+1, direction)
	}
	sql := "SELECT " + strings.Join(selects, ", ") +
		"\nFROM " + relation +
		"\nWHERE " + strings.Join(wheres, "\n  AND ") +
		"\nGROUP BY " + strings.Join(groups, ", ") +
		"\nORDER BY " + strings.Join(orders, ", ")
	return sql, args, nil
}

func (q *FrameQuery[T]) validate(gapfill bool) error {
	if err := q.source.validate(); err != nil {
		return err
	}
	if err := q.timeColumn.validate(); err != nil {
		return err
	}
	if err := q.bucket.validate(gapfill); err != nil {
		return err
	}
	if !q.rangeSet {
		if gapfill {
			return fmt.Errorf("timescaledb: bounded Between range is required for gapfill")
		}
		return fmt.Errorf("timescaledb: Between range is required")
	}
	if !q.start.Before(q.end) {
		return fmt.Errorf("timescaledb: frame range start must be before end")
	}
	if len(q.measures) == 0 {
		return fmt.Errorf("timescaledb: at least one measure is required")
	}
	seenDimensions := make(map[Column]struct{}, len(q.dimensions))
	seenOutputs := map[string]struct{}{"bucket": {}}
	for _, dimension := range q.dimensions {
		if err := dimension.validate(); err != nil {
			return err
		}
		if _, exists := seenDimensions[dimension]; exists {
			return fmt.Errorf("timescaledb: duplicate dimension %q", dimension)
		}
		seenDimensions[dimension] = struct{}{}
		name := string(dimension)
		if _, exists := seenOutputs[name]; exists {
			return fmt.Errorf("timescaledb: duplicate output name %q", name)
		}
		seenOutputs[name] = struct{}{}
	}
	for _, measure := range q.measures {
		if err := measure.validate(); err != nil {
			return err
		}
		name := string(measure.alias)
		if _, exists := seenOutputs[name]; exists {
			return fmt.Errorf("timescaledb: duplicate measure alias %q", name)
		}
		seenOutputs[name] = struct{}{}
	}
	for _, predicate := range q.predicates {
		if _, err := predicate.sql(new([]any)); err != nil {
			return err
		}
	}
	return nil
}

// Execute runs the built query and collects rows using
// pgx.RowToAddrOfStructByName. A successful empty query returns a non-nil slice.
func (q *FrameQuery[T]) Execute(ctx context.Context, db *pgxext.DataSource) ([]*T, error) {
	sql, args, err := q.Build()
	if err != nil {
		return nil, fmt.Errorf("timescaledb: build frame query for %s: %w", relationContext(q.source), err)
	}
	if ctx == nil {
		return nil, fmt.Errorf("timescaledb: execute frame query for %s: nil context", relationContext(q.source))
	}
	if db == nil {
		return nil, fmt.Errorf("timescaledb: execute frame query for %s: nil DataSource", relationContext(q.source))
	}
	rows, err := db.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("timescaledb: execute frame query for %s: %w", relationContext(q.source), err)
	}
	result, err := pgx.CollectRows(rows, pgx.RowToAddrOfStructByName[T])
	if err != nil {
		return nil, fmt.Errorf("timescaledb: collect frame query for %s: %w", relationContext(q.source), err)
	}
	if result == nil {
		result = make([]*T, 0)
	}
	return result, nil
}
