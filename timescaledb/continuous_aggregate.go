package timescaledb

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/0x626f/pgxext"
)

// ContinuousAggregate builds a TimescaleDB continuous aggregate definition.
// It defaults to WITH NO DATA and materialized-only reads.
type ContinuousAggregate struct {
	view               Relation
	source             Relation
	timeColumn         Column
	bucket             bucketConfig
	bucketAlias        Alias
	dimensions         []Column
	measures           []Measure
	trustedSelect      TrustedExpression
	ifNotExists        bool
	withNoData         bool
	createGroupIndexes bool
	materializedOnly   bool
}

func NewContinuousAggregate(view, source Relation, timeColumn Column) *ContinuousAggregate {
	return &ContinuousAggregate{
		view:               view,
		source:             source,
		timeColumn:         timeColumn,
		bucketAlias:        "bucket",
		withNoData:         true,
		createGroupIndexes: true,
		materializedOnly:   true,
	}
}

func (a *ContinuousAggregate) Relation() Relation { return a.view }

func (a *ContinuousAggregate) Bucket(width Interval) *ContinuousAggregate {
	a.bucket.width = width
	return a
}

func (a *ContinuousAggregate) BucketAlias(alias Alias) *ContinuousAggregate {
	a.bucketAlias = alias
	return a
}

func (a *ContinuousAggregate) Dimension(column Column) *ContinuousAggregate {
	a.dimensions = append(a.dimensions, column)
	return a
}

func (a *ContinuousAggregate) Measure(measures ...Measure) *ContinuousAggregate {
	a.measures = append(a.measures, measures...)
	return a
}

func (a *ContinuousAggregate) Timezone(timezone string) *ContinuousAggregate {
	a.bucket.timezone = timezone
	return a
}

func (a *ContinuousAggregate) Origin(origin time.Time) *ContinuousAggregate {
	value := origin
	a.bucket.origin = &value
	return a
}

func (a *ContinuousAggregate) Offset(offset Interval) *ContinuousAggregate {
	a.bucket.offset = offset
	a.bucket.offsetSet = true
	return a
}

// Select uses a fully trusted static SELECT for advanced definitions. The
// package still rejects time_bucket_gapfill, which TimescaleDB does not allow
// directly in a continuous aggregate.
func (a *ContinuousAggregate) Select(query TrustedExpression) *ContinuousAggregate {
	a.trustedSelect = query
	return a
}

// IfNotExists avoids an error for an existing name. It does not reconcile or
// replace a changed continuous aggregate definition.
func (a *ContinuousAggregate) IfNotExists() *ContinuousAggregate {
	a.ifNotExists = true
	return a
}

func (a *ContinuousAggregate) WithNoData() *ContinuousAggregate {
	a.withNoData = true
	return a
}

// WithData explicitly opts into materializing all eligible history at create
// time. WITH NO DATA is the default.
func (a *ContinuousAggregate) WithData() *ContinuousAggregate {
	a.withNoData = false
	return a
}

func (a *ContinuousAggregate) CreateGroupIndexes(enabled bool) *ContinuousAggregate {
	a.createGroupIndexes = enabled
	return a
}

// RealTime enables or disables real-time aggregation. Enabled maps to
// timescaledb.materialized_only=false.
func (a *ContinuousAggregate) RealTime(enabled bool) *ContinuousAggregate {
	a.materializedOnly = !enabled
	return a
}

// MaterializedOnly directly controls the Timescale storage parameter.
func (a *ContinuousAggregate) MaterializedOnly(enabled bool) *ContinuousAggregate {
	a.materializedOnly = enabled
	return a
}

func (a *ContinuousAggregate) BuildSQL() (string, error) {
	selectSQL, err := a.buildSelect()
	if err != nil {
		return "", err
	}
	view, _ := a.view.quoted()
	prefix := "CREATE MATERIALIZED VIEW "
	if a.ifNotExists {
		prefix += "IF NOT EXISTS "
	}
	sql := fmt.Sprintf(
		"%s%s\nWITH (\n  timescaledb.continuous,\n  timescaledb.create_group_indexes = %s,\n  timescaledb.materialized_only = %s\n) AS\n%s",
		prefix, view, boolSQL(a.createGroupIndexes), boolSQL(a.materializedOnly), selectSQL,
	)
	if a.withNoData {
		sql += "\nWITH NO DATA;"
	} else {
		sql += "\nWITH DATA;"
	}
	return sql, nil
}

func (a *ContinuousAggregate) buildSelect() (string, error) {
	if err := a.view.validate(); err != nil {
		return "", err
	}
	if err := a.source.validate(); err != nil {
		return "", err
	}
	if a.trustedSelect != "" {
		if err := validateTrusted("continuous aggregate SELECT", a.trustedSelect); err != nil {
			return "", err
		}
		query := strings.TrimSpace(string(a.trustedSelect))
		query = strings.TrimSpace(strings.TrimSuffix(query, ";"))
		if query == "" {
			return "", fmt.Errorf("timescaledb: empty trusted continuous aggregate SELECT")
		}
		if strings.Contains(strings.ToLower(query), "time_bucket_gapfill") {
			return "", fmt.Errorf("timescaledb: time_bucket_gapfill is not allowed in a continuous aggregate definition")
		}
		return query, nil
	}
	if err := a.timeColumn.validate(); err != nil {
		return "", err
	}
	if err := a.bucketAlias.validate(); err != nil {
		return "", err
	}
	if err := a.bucket.validate(false); err != nil {
		return "", err
	}
	if len(a.measures) == 0 {
		return "", fmt.Errorf("timescaledb: at least one continuous aggregate measure is required")
	}
	timeColumn, _ := a.timeColumn.quoted()
	bucket, err := a.bucket.literalSQL(timeColumn)
	if err != nil {
		return "", err
	}
	bucketAlias, _ := a.bucketAlias.quoted()
	selects := []string{bucket + " AS " + bucketAlias}
	seenDimensions := make(map[Column]struct{}, len(a.dimensions))
	seenOutputs := map[string]struct{}{string(a.bucketAlias): {}}
	for _, dimension := range a.dimensions {
		if err := dimension.validate(); err != nil {
			return "", err
		}
		if _, exists := seenDimensions[dimension]; exists {
			return "", fmt.Errorf("timescaledb: duplicate dimension %q", dimension)
		}
		seenDimensions[dimension] = struct{}{}
		if _, exists := seenOutputs[string(dimension)]; exists {
			return "", fmt.Errorf("timescaledb: duplicate output name %q", dimension)
		}
		seenOutputs[string(dimension)] = struct{}{}
		quoted, _ := dimension.quoted()
		selects = append(selects, quoted)
	}
	for _, measure := range a.measures {
		if err := measure.validate(); err != nil {
			return "", err
		}
		if measure.fill != fillNull {
			return "", fmt.Errorf("timescaledb: gap filling is not allowed in a continuous aggregate definition")
		}
		if _, exists := seenOutputs[string(measure.alias)]; exists {
			return "", fmt.Errorf("timescaledb: duplicate measure alias %q", measure.alias)
		}
		seenOutputs[string(measure.alias)] = struct{}{}
		aggregate, err := measure.aggregateSQL()
		if err != nil {
			return "", err
		}
		alias, _ := measure.alias.quoted()
		selects = append(selects, aggregate+" AS "+alias)
	}
	source, _ := a.source.quoted()
	groups := make([]string, 1+len(a.dimensions))
	for index := range groups {
		groups[index] = fmt.Sprintf("%d", index+1)
	}
	return "SELECT " + strings.Join(selects, ", ") +
		"\nFROM " + source +
		"\nGROUP BY " + strings.Join(groups, ", "), nil
}

func (a *ContinuousAggregate) Apply(ctx context.Context, db *pgxext.DataSource) error {
	sql, err := a.BuildSQL()
	if err != nil {
		return fmt.Errorf("timescaledb: build continuous aggregate %s: %w", relationContext(a.view), err)
	}
	if ctx == nil {
		return fmt.Errorf("timescaledb: create continuous aggregate %s: nil context", relationContext(a.view))
	}
	if db == nil {
		return fmt.Errorf("timescaledb: create continuous aggregate %s: nil DataSource", relationContext(a.view))
	}
	if _, err := db.Exec(ctx, sql); err != nil {
		return fmt.Errorf("timescaledb: create continuous aggregate %s: %w", relationContext(a.view), err)
	}
	return nil
}

func (a *ContinuousAggregate) DropSQL(ifExists bool) (string, error) {
	view, err := a.view.quoted()
	if err != nil {
		return "", err
	}
	if ifExists {
		return "DROP MATERIALIZED VIEW IF EXISTS " + view + ";", nil
	}
	return "DROP MATERIALIZED VIEW " + view + ";", nil
}

func (a *ContinuousAggregate) Drop(ctx context.Context, db *pgxext.DataSource, ifExists bool) error {
	sql, err := a.DropSQL(ifExists)
	if err != nil {
		return fmt.Errorf("timescaledb: build drop continuous aggregate %s: %w", relationContext(a.view), err)
	}
	if ctx == nil {
		return fmt.Errorf("timescaledb: drop continuous aggregate %s: nil context", relationContext(a.view))
	}
	if db == nil {
		return fmt.Errorf("timescaledb: drop continuous aggregate %s: nil DataSource", relationContext(a.view))
	}
	if _, err := db.Exec(ctx, sql); err != nil {
		return fmt.Errorf("timescaledb: drop continuous aggregate %s: %w", relationContext(a.view), err)
	}
	return nil
}

// SetRealTime changes real-time aggregation at runtime. Real-time reads combine
// materialized history with newer raw rows; they are not push/streaming updates.
// Historical rows before the watermark still require a refresh.
func SetRealTime(ctx context.Context, db *pgxext.DataSource, view Relation, enabled bool) error {
	sql, err := buildSetRealTimeSQL(view, enabled)
	if err != nil {
		return fmt.Errorf("timescaledb: set real-time aggregation for %s: %w", relationContext(view), err)
	}
	if ctx == nil {
		return fmt.Errorf("timescaledb: set real-time aggregation for %s: nil context", relationContext(view))
	}
	if db == nil {
		return fmt.Errorf("timescaledb: set real-time aggregation for %s: nil DataSource", relationContext(view))
	}
	if _, err := db.Exec(ctx, sql); err != nil {
		return fmt.Errorf("timescaledb: set real-time aggregation for %s: %w", relationContext(view), err)
	}
	return nil
}

func buildSetRealTimeSQL(view Relation, enabled bool) (string, error) {
	name, err := view.quoted()
	if err != nil {
		return "", err
	}
	materializedOnly := !enabled
	return fmt.Sprintf("ALTER MATERIALIZED VIEW %s SET (timescaledb.materialized_only = %s);", name, boolSQL(materializedOnly)), nil
}

func (a *ContinuousAggregate) SetRealTime(ctx context.Context, db *pgxext.DataSource, enabled bool) error {
	return SetRealTime(ctx, db, a.view, enabled)
}

func (a *ContinuousAggregate) ReconcileRefreshPolicy(ctx context.Context, db *pgxext.DataSource, policy RefreshPolicy) (PolicyAction, error) {
	return ReconcileRefreshPolicy(ctx, db, a.view, policy)
}

func (a *ContinuousAggregate) EnsureRefreshPolicy(ctx context.Context, db *pgxext.DataSource, policy RefreshPolicy) (PolicyAction, error) {
	return EnsureRefreshPolicy(ctx, db, a.view, policy)
}

func (a *ContinuousAggregate) Refresh(ctx context.Context, db *pgxext.DataSource, start, end time.Time, force bool) error {
	return RefreshContinuousAggregate(ctx, db, a.view, start, end, force)
}
