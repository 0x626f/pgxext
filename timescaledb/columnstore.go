package timescaledb

import (
	"context"
	"fmt"
	"strings"

	"github.com/0x626f/pgxext"
)

// ColumnStoreOrder is one safe columnstore ordering term.
type ColumnStoreOrder struct {
	column         Column
	descending     bool
	nullsFirst     bool
	nullsSpecified bool
}

// ColumnstoreAscending orders a column ascending.
func ColumnstoreAscending(column Column) ColumnStoreOrder {
	return ColumnStoreOrder{column: column}
}

// ColumnstoreDescending orders a column descending.
func ColumnstoreDescending(column Column) ColumnStoreOrder {
	return ColumnStoreOrder{column: column, descending: true}
}

func (o ColumnStoreOrder) NullsFirst() ColumnStoreOrder {
	o.nullsFirst = true
	o.nullsSpecified = true
	return o
}

func (o ColumnStoreOrder) NullsLast() ColumnStoreOrder {
	o.nullsFirst = false
	o.nullsSpecified = true
	return o
}

// ColumnstoreSettings contains safe segment-by and order-by identifiers.
type ColumnstoreSettings struct {
	segmentBy []Column
	orderBy   []ColumnStoreOrder
}

func NewColumnstoreSettings() ColumnstoreSettings {
	return ColumnstoreSettings{}
}

func (s ColumnstoreSettings) SegmentBy(columns ...Column) ColumnstoreSettings {
	s.segmentBy = append([]Column(nil), columns...)
	return s
}

func (s ColumnstoreSettings) OrderBy(columns ...ColumnStoreOrder) ColumnstoreSettings {
	s.orderBy = append([]ColumnStoreOrder(nil), columns...)
	return s
}

func (s ColumnstoreSettings) validate() error {
	seenSegments := make(map[Column]struct{}, len(s.segmentBy))
	for _, column := range s.segmentBy {
		if err := column.validate(); err != nil {
			return err
		}
		if _, exists := seenSegments[column]; exists {
			return fmt.Errorf("timescaledb: duplicate columnstore segment column %q", column)
		}
		seenSegments[column] = struct{}{}
	}
	seenOrders := make(map[Column]struct{}, len(s.orderBy))
	for _, order := range s.orderBy {
		if err := order.column.validate(); err != nil {
			return err
		}
		if _, exists := seenOrders[order.column]; exists {
			return fmt.Errorf("timescaledb: duplicate columnstore order column %q", order.column)
		}
		seenOrders[order.column] = struct{}{}
	}
	return nil
}

func (s ColumnstoreSettings) optionValues() (string, string, error) {
	if err := s.validate(); err != nil {
		return "", "", err
	}
	segments := make([]string, len(s.segmentBy))
	for index, column := range s.segmentBy {
		segments[index], _ = column.quoted()
	}
	orders := make([]string, len(s.orderBy))
	for index, order := range s.orderBy {
		column, _ := order.column.quoted()
		direction := "ASC"
		if order.descending {
			direction = "DESC"
		}
		orders[index] = column + " " + direction
		if order.nullsSpecified {
			if order.nullsFirst {
				orders[index] += " NULLS FIRST"
			} else {
				orders[index] += " NULLS LAST"
			}
		}
	}
	return strings.Join(segments, ", "), strings.Join(orders, ", "), nil
}

func buildSetColumnstoreSQL(relation Relation, materializedView, enabled bool, settings ColumnstoreSettings) (string, error) {
	name, err := relation.quoted()
	if err != nil {
		return "", err
	}
	segmentBy, orderBy, err := settings.optionValues()
	if err != nil {
		return "", err
	}
	options := []string{"timescaledb.enable_columnstore = " + boolSQL(enabled)}
	if enabled && segmentBy != "" {
		options = append(options, "timescaledb.segmentby = "+sqlLiteral(segmentBy))
	}
	if enabled && orderBy != "" {
		options = append(options, "timescaledb.orderby = "+sqlLiteral(orderBy))
	}
	kind := "TABLE"
	if materializedView {
		kind = "MATERIALIZED VIEW"
	}
	return fmt.Sprintf("ALTER %s %s SET (%s);", kind, name, strings.Join(options, ", ")), nil
}

// BuildEnableHypertableColumnstoreSQL builds migration-safe ALTER TABLE SQL.
func BuildEnableHypertableColumnstoreSQL(relation Relation, settings ColumnstoreSettings) (string, error) {
	return buildSetColumnstoreSQL(relation, false, true, settings)
}

// BuildEnableContinuousAggregateColumnstoreSQL builds migration-safe ALTER
// MATERIALIZED VIEW SQL for a continuous aggregate.
func BuildEnableContinuousAggregateColumnstoreSQL(relation Relation, settings ColumnstoreSettings) (string, error) {
	return buildSetColumnstoreSQL(relation, true, true, settings)
}

func setColumnstore(ctx context.Context, db *pgxext.DataSource, relation Relation, materializedView, enabled bool, settings ColumnstoreSettings) error {
	sql, err := buildSetColumnstoreSQL(relation, materializedView, enabled, settings)
	if err != nil {
		return fmt.Errorf("timescaledb: build columnstore settings for %s: %w", relationContext(relation), err)
	}
	if ctx == nil {
		return fmt.Errorf("timescaledb: set columnstore for %s: nil context", relationContext(relation))
	}
	if db == nil {
		return fmt.Errorf("timescaledb: set columnstore for %s: nil DataSource", relationContext(relation))
	}
	if _, err := requireCapability(ctx, db, "columnstore"); err != nil {
		return fmt.Errorf("timescaledb: set columnstore for %s: %w", relationContext(relation), err)
	}
	if _, err := db.Exec(ctx, sql); err != nil {
		return fmt.Errorf("timescaledb: set columnstore for %s: %w", relationContext(relation), err)
	}
	return nil
}

func EnableHypertableColumnstore(ctx context.Context, db *pgxext.DataSource, relation Relation, settings ColumnstoreSettings) error {
	return setColumnstore(ctx, db, relation, false, true, settings)
}

func DisableHypertableColumnstore(ctx context.Context, db *pgxext.DataSource, relation Relation) error {
	return setColumnstore(ctx, db, relation, false, false, ColumnstoreSettings{})
}

func EnableContinuousAggregateColumnstore(ctx context.Context, db *pgxext.DataSource, relation Relation, settings ColumnstoreSettings) error {
	return setColumnstore(ctx, db, relation, true, true, settings)
}

func DisableContinuousAggregateColumnstore(ctx context.Context, db *pgxext.DataSource, relation Relation) error {
	return setColumnstore(ctx, db, relation, true, false, ColumnstoreSettings{})
}
