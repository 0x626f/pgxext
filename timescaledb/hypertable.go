package timescaledb

import (
	"context"
	"fmt"
	"strings"

	"github.com/0x626f/pgxext"
)

type hypertableOptions struct {
	chunkInterval        Interval
	chunkIntervalSet     bool
	ifNotExists          bool
	createDefaultIndexes bool
	migrateData          bool
	hashColumn           Column
	hashPartitions       int
	columnstore          *ColumnstoreSettings
}

// Hypertable converts an existing PostgreSQL table with the generalized stable
// create_hypertable API.
type Hypertable struct {
	relation   Relation
	timeColumn Column
	options    hypertableOptions
}

// NewHypertable creates an existing-table conversion builder. It does not
// create the underlying application table.
func NewHypertable(relation Relation, timeColumn Column) *Hypertable {
	return &Hypertable{
		relation:   relation,
		timeColumn: timeColumn,
		options:    hypertableOptions{createDefaultIndexes: true},
	}
}

func (h *Hypertable) Relation() Relation { return h.relation }

func (h *Hypertable) ChunkInterval(interval Interval) *Hypertable {
	h.options.chunkInterval = interval
	h.options.chunkIntervalSet = true
	return h
}

func (h *Hypertable) IfNotExists() *Hypertable {
	h.options.ifNotExists = true
	return h
}

func (h *Hypertable) CreateDefaultIndexes(enabled bool) *Hypertable {
	h.options.createDefaultIndexes = enabled
	return h
}

func (h *Hypertable) MigrateExistingData() *Hypertable {
	h.options.migrateData = true
	return h
}

// HashDimension adds an opt-in secondary hash dimension. Extra dimensions are
// generally useful only for parallel I/O across multiple tablespaces and
// should not be the default.
func (h *Hypertable) HashDimension(column Column, partitions int) *Hypertable {
	h.options.hashColumn = column
	h.options.hashPartitions = partitions
	return h
}

func (h *Hypertable) Columnstore(settings ColumnstoreSettings) *Hypertable {
	copy := settings
	h.options.columnstore = &copy
	return h
}

// BuildSQL returns conversion SQL suitable for an application migration.
// Conversion is intentionally not presented as reversible.
func (h *Hypertable) BuildSQL() (string, error) {
	if err := validateHypertableOptions(h.relation, h.timeColumn, h.options); err != nil {
		return "", err
	}
	regclass, _ := h.relation.regclassText()
	dimension := "by_range(" + sqlLiteral(string(h.timeColumn))
	if h.options.chunkIntervalSet {
		interval, _ := h.options.chunkInterval.SQL()
		dimension += ", " + interval
	}
	dimension += ")"
	statements := []string{fmt.Sprintf(
		"SELECT create_hypertable(%s::regclass, %s, create_default_indexes => %s, if_not_exists => %s, migrate_data => %s);",
		sqlLiteral(regclass), dimension, boolSQL(h.options.createDefaultIndexes), boolSQL(h.options.ifNotExists), boolSQL(h.options.migrateData),
	)}
	if h.options.hashColumn != "" {
		statements = append(statements, fmt.Sprintf(
			"SELECT add_dimension(%s::regclass, by_hash(%s, %d), if_not_exists => %s);",
			sqlLiteral(regclass), sqlLiteral(string(h.options.hashColumn)), h.options.hashPartitions, boolSQL(h.options.ifNotExists),
		))
	}
	if h.options.columnstore != nil {
		alter, err := BuildEnableHypertableColumnstoreSQL(h.relation, *h.options.columnstore)
		if err != nil {
			return "", err
		}
		statements = append(statements, alter)
	}
	return strings.Join(statements, "\n"), nil
}

func (h *Hypertable) Apply(ctx context.Context, db *pgxext.DataSource) error {
	sql, err := h.BuildSQL()
	if err != nil {
		return fmt.Errorf("timescaledb: build hypertable conversion for %s: %w", relationContext(h.relation), err)
	}
	if ctx == nil {
		return fmt.Errorf("timescaledb: convert hypertable %s: nil context", relationContext(h.relation))
	}
	if db == nil {
		return fmt.Errorf("timescaledb: convert hypertable %s: nil DataSource", relationContext(h.relation))
	}
	if h.options.columnstore != nil {
		if _, err := requireCapability(ctx, db, "columnstore"); err != nil {
			return fmt.Errorf("timescaledb: convert hypertable %s: %w", relationContext(h.relation), err)
		}
	}
	if _, err := db.Exec(ctx, sql); err != nil {
		return fmt.Errorf("timescaledb: convert hypertable %s: %w", relationContext(h.relation), err)
	}
	return nil
}

// HypertableTable creates a new application-defined table using TimescaleDB
// 2.20's modern CREATE TABLE storage options.
type HypertableTable struct {
	relation   Relation
	timeColumn Column
	definition TrustedExpression
	options    hypertableOptions
}

// NewHypertableTable creates a modern CREATE TABLE builder. definition is a
// trusted, static column/constraint definition owned by the application.
func NewHypertableTable(relation Relation, timeColumn Column, definition TrustedExpression) *HypertableTable {
	return &HypertableTable{
		relation: relation, timeColumn: timeColumn, definition: definition,
		options: hypertableOptions{createDefaultIndexes: true},
	}
}

func (h *HypertableTable) Relation() Relation { return h.relation }

func (h *HypertableTable) ChunkInterval(interval Interval) *HypertableTable {
	h.options.chunkInterval, h.options.chunkIntervalSet = interval, true
	return h
}

func (h *HypertableTable) IfNotExists() *HypertableTable {
	h.options.ifNotExists = true
	return h
}

func (h *HypertableTable) CreateDefaultIndexes(enabled bool) *HypertableTable {
	h.options.createDefaultIndexes = enabled
	return h
}

func (h *HypertableTable) HashDimension(column Column, partitions int) *HypertableTable {
	h.options.hashColumn, h.options.hashPartitions = column, partitions
	return h
}

func (h *HypertableTable) Columnstore(settings ColumnstoreSettings) *HypertableTable {
	copy := settings
	h.options.columnstore = &copy
	return h
}

// BuildSQL returns modern CREATE TABLE SQL and, when configured, a stable
// add_dimension statement for secondary hash partitioning.
func (h *HypertableTable) BuildSQL() (string, error) {
	if err := validateHypertableOptions(h.relation, h.timeColumn, h.options); err != nil {
		return "", err
	}
	if h.options.migrateData {
		return "", fmt.Errorf("timescaledb: migrate_data is not valid for a new table")
	}
	if err := validateTrusted("table definition", h.definition); err != nil {
		return "", err
	}
	relation, _ := h.relation.quoted()
	options := []string{
		"tsdb.hypertable",
		"tsdb.partition_column = " + sqlLiteral(string(h.timeColumn)),
		"tsdb.create_default_indexes = " + boolSQL(h.options.createDefaultIndexes),
	}
	if h.options.chunkIntervalSet {
		value, _ := h.options.chunkInterval.optionText()
		options = append(options, "tsdb.chunk_interval = "+sqlLiteral(value))
	}
	if h.options.columnstore != nil {
		segmentBy, orderBy, err := h.options.columnstore.optionValues()
		if err != nil {
			return "", err
		}
		if segmentBy != "" {
			options = append(options, "tsdb.segmentby = "+sqlLiteral(segmentBy))
		}
		if orderBy != "" {
			options = append(options, "tsdb.orderby = "+sqlLiteral(orderBy))
		}
	}
	prefix := "CREATE TABLE "
	if h.options.ifNotExists {
		prefix += "IF NOT EXISTS "
	}
	statements := []string{fmt.Sprintf("%s%s (\n%s\n) WITH (\n  %s\n);", prefix, relation, strings.TrimSpace(string(h.definition)), strings.Join(options, ",\n  "))}
	if h.options.hashColumn != "" {
		regclass, _ := h.relation.regclassText()
		statements = append(statements, fmt.Sprintf(
			"SELECT add_dimension(%s::regclass, by_hash(%s, %d), if_not_exists => %s);",
			sqlLiteral(regclass), sqlLiteral(string(h.options.hashColumn)), h.options.hashPartitions, boolSQL(h.options.ifNotExists),
		))
	}
	return strings.Join(statements, "\n"), nil
}

func (h *HypertableTable) Apply(ctx context.Context, db *pgxext.DataSource) error {
	sql, err := h.BuildSQL()
	if err != nil {
		return fmt.Errorf("timescaledb: build modern hypertable %s: %w", relationContext(h.relation), err)
	}
	if ctx == nil {
		return fmt.Errorf("timescaledb: create modern hypertable %s: nil context", relationContext(h.relation))
	}
	if db == nil {
		return fmt.Errorf("timescaledb: create modern hypertable %s: nil DataSource", relationContext(h.relation))
	}
	if _, err := requireCapability(ctx, db, "modern hypertable CREATE TABLE"); err != nil {
		return fmt.Errorf("timescaledb: create modern hypertable %s: %w", relationContext(h.relation), err)
	}
	if _, err := db.Exec(ctx, sql); err != nil {
		return fmt.Errorf("timescaledb: create modern hypertable %s: %w", relationContext(h.relation), err)
	}
	return nil
}

func validateHypertableOptions(relation Relation, timeColumn Column, options hypertableOptions) error {
	if err := relation.validate(); err != nil {
		return err
	}
	if err := timeColumn.validate(); err != nil {
		return err
	}
	if options.chunkIntervalSet {
		if err := options.chunkInterval.Validate(); err != nil {
			return fmt.Errorf("timescaledb: invalid chunk interval: %w", err)
		}
	}
	if options.hashColumn != "" {
		if err := options.hashColumn.validate(); err != nil {
			return err
		}
		if options.hashPartitions <= 0 {
			return fmt.Errorf("timescaledb: hash partition count must be positive")
		}
	} else if options.hashPartitions != 0 {
		return fmt.Errorf("timescaledb: hash partition column is required")
	}
	if options.columnstore != nil {
		if err := options.columnstore.validate(); err != nil {
			return err
		}
	}
	return nil
}

// SetChunkIntervalSQL returns migration-safe SQL. Changing a chunk interval
// affects only chunks created after the change.
func SetChunkIntervalSQL(relation Relation, interval Interval) (string, error) {
	regclass, err := relation.regclassText()
	if err != nil {
		return "", err
	}
	value, err := interval.SQL()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("SELECT set_chunk_time_interval(%s::regclass, %s);", sqlLiteral(regclass), value), nil
}

// SetChunkInterval changes the interval for future chunks only.
func SetChunkInterval(ctx context.Context, db *pgxext.DataSource, relation Relation, interval Interval) error {
	if _, err := SetChunkIntervalSQL(relation, interval); err != nil {
		return fmt.Errorf("timescaledb: set chunk interval for %s: %w", relationContext(relation), err)
	}
	if ctx == nil {
		return fmt.Errorf("timescaledb: set chunk interval for %s: nil context", relationContext(relation))
	}
	if db == nil {
		return fmt.Errorf("timescaledb: set chunk interval for %s: nil DataSource", relationContext(relation))
	}
	isHypertable, err := IsHypertable(ctx, db, relation)
	if err != nil {
		return fmt.Errorf("timescaledb: set chunk interval for %s: %w", relationContext(relation), err)
	}
	if !isHypertable {
		return fmt.Errorf("timescaledb: set chunk interval for %s: %w", relationContext(relation), ErrNotHypertable)
	}
	regclass, _ := relation.regclassText()
	value, _ := interval.pgValue()
	if _, err := db.Exec(ctx, `SELECT set_chunk_time_interval($1::regclass, $2::interval)`, regclass, value); err != nil {
		return fmt.Errorf("timescaledb: set chunk interval for %s: %w", relationContext(relation), err)
	}
	return nil
}

// IsHypertable checks the documented hypertables information view.
func IsHypertable(ctx context.Context, db *pgxext.DataSource, relation Relation) (bool, error) {
	regclass, err := relation.regclassText()
	if err != nil {
		return false, fmt.Errorf("timescaledb: inspect hypertable %s: %w", relationContext(relation), err)
	}
	if ctx == nil {
		return false, fmt.Errorf("timescaledb: inspect hypertable %s: nil context", relationContext(relation))
	}
	if db == nil {
		return false, fmt.Errorf("timescaledb: inspect hypertable %s: nil DataSource", relationContext(relation))
	}
	rows, err := db.Query(ctx, `
SELECT EXISTS (
  SELECT 1
  FROM timescaledb_information.hypertables h
  WHERE format('%I.%I', h.hypertable_schema, h.hypertable_name)::regclass = to_regclass($1)
)`, regclass)
	if err != nil {
		return false, fmt.Errorf("timescaledb: inspect hypertable %s: %w", relationContext(relation), err)
	}
	defer closeRows(rows)
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return false, fmt.Errorf("timescaledb: inspect hypertable %s: %w", relationContext(relation), err)
		}
		return false, fmt.Errorf("timescaledb: inspect hypertable %s: no result", relationContext(relation))
	}
	var result bool
	if err := rows.Scan(&result); err != nil {
		return false, fmt.Errorf("timescaledb: inspect hypertable %s: %w", relationContext(relation), err)
	}
	return result, nil
}
