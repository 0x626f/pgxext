package timescaledb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/0x626f/pgxext"
	"github.com/jackc/pgx/v5/pgtype"
)

// HypertableMetadata is read from timescaledb_information.hypertables.
type HypertableMetadata struct {
	Relation             Relation
	Owner                string
	NumDimensions        int
	NumChunks            int64
	ColumnstoreEnabled   bool
	Tablespaces          []string
	PrimaryDimension     Column
	PrimaryDimensionType string
	Dimensions           []DimensionMetadata
}

// DimensionMetadata is read from timescaledb_information.dimensions. Integer
// fields are exposed for observability only; package operations support only
// TIMESTAMPTZ time dimensions.
type DimensionMetadata struct {
	Relation        Relation
	Number          int64
	Column          Column
	ColumnType      string
	Type            string
	TimeInterval    Interval
	IntegerInterval *int64
	IntegerNowFunc  string
	NumPartitions   *int16
}

// ChunkMetadata is read from timescaledb_information.chunks.
type ChunkMetadata struct {
	Hypertable           Relation
	Chunk                Relation
	PrimaryDimension     Column
	PrimaryDimensionType string
	RangeStart           *time.Time
	RangeEnd             *time.Time
	IntegerRangeStart    *int64
	IntegerRangeEnd      *int64
	IsColumnstore        bool
	// IsCompressed mirrors the information-view column name for compatibility.
	IsCompressed bool
	Tablespace   string
	CreatedAt    *time.Time
}

// ContinuousAggregateMetadata is read from
// timescaledb_information.continuous_aggregates.
type ContinuousAggregateMetadata struct {
	Source                    Relation
	View                      Relation
	Owner                     string
	MaterializedOnly          bool
	ColumnstoreEnabled        bool
	MaterializationHypertable Relation
	ViewDefinition            string
	Finalized                 bool
}

// JobMetadata is read from timescaledb_information.jobs. TimescaleDB 2.20 does
// not expose timezone in this public view; policy inspection supplements it in
// one isolated compatibility query.
type JobMetadata struct {
	ID               int64
	ApplicationName  string
	ScheduleInterval Interval
	MaxRuntime       Interval
	MaxRetries       int
	RetryPeriod      Interval
	ProcedureSchema  string
	ProcedureName    string
	Owner            string
	Scheduled        bool
	FixedSchedule    bool
	Config           json.RawMessage
	NextRun          *time.Time
	InitialStart     *time.Time
	Relation         *Relation
	CheckSchema      string
	CheckName        string
}

// JobStats is read from timescaledb_information.job_stats.
type JobStats struct {
	JobID                int64
	Relation             *Relation
	LastRunStartedAt     *time.Time
	LastSuccessfulFinish *time.Time
	LastRunStatus        string
	JobStatus            string
	LastRunDuration      *time.Duration
	NextRun              *time.Time
	TotalRuns            int64
	TotalSuccesses       int64
	TotalFailures        int64
}

// PolicyKind identifies a Timescale background policy procedure.
type PolicyKind string

const (
	RefreshPolicyKind     PolicyKind = "refresh"
	RetentionPolicyKind   PolicyKind = "retention"
	ColumnstorePolicyKind PolicyKind = "columnstore"
)

func ListHypertables(ctx context.Context, db *pgxext.DataSource) ([]HypertableMetadata, error) {
	if ctx == nil {
		return nil, fmt.Errorf("timescaledb: list hypertables: nil context")
	}
	if db == nil {
		return nil, fmt.Errorf("timescaledb: list hypertables: nil DataSource")
	}
	rows, err := db.Query(ctx, `
SELECT hypertable_schema, hypertable_name, owner, num_dimensions, num_chunks,
       compression_enabled, COALESCE(tablespaces::text[], ARRAY[]::text[]),
       primary_dimension, primary_dimension_type::text
FROM timescaledb_information.hypertables
ORDER BY hypertable_schema, hypertable_name`)
	if err != nil {
		return nil, fmt.Errorf("timescaledb: list hypertables: %w", err)
	}
	defer closeRows(rows)
	result := make([]HypertableMetadata, 0)
	for rows.Next() {
		var item HypertableMetadata
		if err := rows.Scan(
			&item.Relation.Schema, &item.Relation.Name, &item.Owner,
			&item.NumDimensions, &item.NumChunks, &item.ColumnstoreEnabled,
			&item.Tablespaces, &item.PrimaryDimension, &item.PrimaryDimensionType,
		); err != nil {
			return nil, fmt.Errorf("timescaledb: scan hypertable metadata: %w", err)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("timescaledb: list hypertables: %w", err)
	}
	dimensions, err := ListDimensions(ctx, db)
	if err != nil {
		return nil, err
	}
	byRelation := make(map[string][]DimensionMetadata)
	for _, dimension := range dimensions {
		key := dimension.Relation.Schema + "\x00" + dimension.Relation.Name
		byRelation[key] = append(byRelation[key], dimension)
	}
	for index := range result {
		key := result[index].Relation.Schema + "\x00" + result[index].Relation.Name
		result[index].Dimensions = append([]DimensionMetadata(nil), byRelation[key]...)
	}
	return result, nil
}

func GetHypertable(ctx context.Context, db *pgxext.DataSource, relation Relation) (*HypertableMetadata, error) {
	if err := relation.validate(); err != nil {
		return nil, fmt.Errorf("timescaledb: get hypertable %s: %w", relationContext(relation), err)
	}
	items, err := ListHypertables(ctx, db)
	if err != nil {
		return nil, fmt.Errorf("timescaledb: get hypertable %s: %w", relationContext(relation), err)
	}
	wanted, found, err := resolveRelation(ctx, db, relation)
	if err != nil {
		return nil, fmt.Errorf("timescaledb: get hypertable %s: %w", relationContext(relation), err)
	}
	if !found {
		return nil, fmt.Errorf("timescaledb: get hypertable %s: %w", relationContext(relation), ErrNotHypertable)
	}
	for index := range items {
		if items[index].Relation == wanted {
			return &items[index], nil
		}
	}
	return nil, fmt.Errorf("timescaledb: get hypertable %s: %w", relationContext(relation), ErrNotHypertable)
}

func ListDimensions(ctx context.Context, db *pgxext.DataSource) ([]DimensionMetadata, error) {
	if ctx == nil {
		return nil, fmt.Errorf("timescaledb: list hypertable dimensions: nil context")
	}
	if db == nil {
		return nil, fmt.Errorf("timescaledb: list hypertable dimensions: nil DataSource")
	}
	rows, err := db.Query(ctx, `
SELECT hypertable_schema, hypertable_name, dimension_number, column_name,
       column_type::text, dimension_type, time_interval, integer_interval,
       integer_now_func, num_partitions
FROM timescaledb_information.dimensions
ORDER BY hypertable_schema, hypertable_name, dimension_number`)
	if err != nil {
		return nil, fmt.Errorf("timescaledb: list hypertable dimensions: %w", err)
	}
	defer closeRows(rows)
	result := make([]DimensionMetadata, 0)
	for rows.Next() {
		var item DimensionMetadata
		var interval pgtype.Interval
		var integerInterval pgtype.Int8
		var integerNow pgtype.Text
		var partitions pgtype.Int2
		if err := rows.Scan(
			&item.Relation.Schema, &item.Relation.Name, &item.Number, &item.Column,
			&item.ColumnType, &item.Type, &interval, &integerInterval, &integerNow, &partitions,
		); err != nil {
			return nil, fmt.Errorf("timescaledb: scan hypertable dimension: %w", err)
		}
		item.TimeInterval, err = intervalFromPG(interval)
		if err != nil {
			return nil, fmt.Errorf("timescaledb: scan hypertable dimension interval: %w", err)
		}
		if integerInterval.Valid {
			value := integerInterval.Int64
			item.IntegerInterval = &value
		}
		if integerNow.Valid {
			item.IntegerNowFunc = integerNow.String
		}
		if partitions.Valid {
			value := partitions.Int16
			item.NumPartitions = &value
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("timescaledb: list hypertable dimensions: %w", err)
	}
	return result, nil
}

func ListChunks(ctx context.Context, db *pgxext.DataSource, relation Relation) ([]ChunkMetadata, error) {
	if err := relation.validate(); err != nil {
		return nil, fmt.Errorf("timescaledb: list chunks for %s: %w", relationContext(relation), err)
	}
	if ctx == nil {
		return nil, fmt.Errorf("timescaledb: list chunks for %s: nil context", relationContext(relation))
	}
	if db == nil {
		return nil, fmt.Errorf("timescaledb: list chunks for %s: nil DataSource", relationContext(relation))
	}
	isHypertable, err := IsHypertable(ctx, db, relation)
	if err != nil {
		return nil, err
	}
	if !isHypertable {
		return nil, fmt.Errorf("timescaledb: list chunks for %s: %w", relationContext(relation), ErrNotHypertable)
	}
	regclass, _ := relation.regclassText()
	rows, err := db.Query(ctx, `
SELECT hypertable_schema, hypertable_name, chunk_schema, chunk_name,
       primary_dimension, primary_dimension_type::text, range_start, range_end,
       range_start_integer, range_end_integer, is_compressed,
       chunk_tablespace, chunk_creation_time
FROM timescaledb_information.chunks c
WHERE format('%I.%I', c.hypertable_schema, c.hypertable_name)::regclass = $1::regclass
ORDER BY range_start NULLS LAST, range_start_integer NULLS LAST, chunk_schema, chunk_name`, regclass)
	if err != nil {
		return nil, fmt.Errorf("timescaledb: list chunks for %s: %w", relationContext(relation), err)
	}
	defer closeRows(rows)
	result := make([]ChunkMetadata, 0)
	for rows.Next() {
		var item ChunkMetadata
		var rangeStart, rangeEnd, created pgtype.Timestamptz
		var integerStart, integerEnd pgtype.Int8
		var tablespace pgtype.Text
		if err := rows.Scan(
			&item.Hypertable.Schema, &item.Hypertable.Name, &item.Chunk.Schema, &item.Chunk.Name,
			&item.PrimaryDimension, &item.PrimaryDimensionType, &rangeStart, &rangeEnd,
			&integerStart, &integerEnd, &item.IsCompressed, &tablespace, &created,
		); err != nil {
			return nil, fmt.Errorf("timescaledb: scan chunk for %s: %w", relationContext(relation), err)
		}
		item.IsColumnstore = item.IsCompressed
		item.RangeStart = timestamptzPointer(rangeStart)
		item.RangeEnd = timestamptzPointer(rangeEnd)
		item.CreatedAt = timestamptzPointer(created)
		item.IntegerRangeStart = int64Pointer(integerStart)
		item.IntegerRangeEnd = int64Pointer(integerEnd)
		if tablespace.Valid {
			item.Tablespace = tablespace.String
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("timescaledb: list chunks for %s: %w", relationContext(relation), err)
	}
	return result, nil
}

func ListContinuousAggregates(ctx context.Context, db *pgxext.DataSource) ([]ContinuousAggregateMetadata, error) {
	if ctx == nil {
		return nil, fmt.Errorf("timescaledb: list continuous aggregates: nil context")
	}
	if db == nil {
		return nil, fmt.Errorf("timescaledb: list continuous aggregates: nil DataSource")
	}
	rows, err := db.Query(ctx, `
SELECT hypertable_schema, hypertable_name, view_schema, view_name, view_owner,
       materialized_only, compression_enabled, materialization_hypertable_schema,
       materialization_hypertable_name, view_definition, finalized
FROM timescaledb_information.continuous_aggregates
ORDER BY view_schema, view_name`)
	if err != nil {
		return nil, fmt.Errorf("timescaledb: list continuous aggregates: %w", err)
	}
	defer closeRows(rows)
	result := make([]ContinuousAggregateMetadata, 0)
	for rows.Next() {
		var item ContinuousAggregateMetadata
		if err := rows.Scan(
			&item.Source.Schema, &item.Source.Name, &item.View.Schema, &item.View.Name,
			&item.Owner, &item.MaterializedOnly, &item.ColumnstoreEnabled,
			&item.MaterializationHypertable.Schema, &item.MaterializationHypertable.Name,
			&item.ViewDefinition, &item.Finalized,
		); err != nil {
			return nil, fmt.Errorf("timescaledb: scan continuous aggregate: %w", err)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("timescaledb: list continuous aggregates: %w", err)
	}
	return result, nil
}

func GetContinuousAggregate(ctx context.Context, db *pgxext.DataSource, view Relation) (*ContinuousAggregateMetadata, error) {
	if err := view.validate(); err != nil {
		return nil, fmt.Errorf("timescaledb: get continuous aggregate %s: %w", relationContext(view), err)
	}
	items, err := ListContinuousAggregates(ctx, db)
	if err != nil {
		return nil, fmt.Errorf("timescaledb: get continuous aggregate %s: %w", relationContext(view), err)
	}
	wanted, found, err := resolveRelation(ctx, db, view)
	if err != nil {
		return nil, fmt.Errorf("timescaledb: get continuous aggregate %s: %w", relationContext(view), err)
	}
	if !found {
		return nil, fmt.Errorf("timescaledb: get continuous aggregate %s: %w", relationContext(view), ErrNotContinuousAggregate)
	}
	for index := range items {
		if items[index].View == wanted {
			return &items[index], nil
		}
	}
	return nil, fmt.Errorf("timescaledb: get continuous aggregate %s: %w", relationContext(view), ErrNotContinuousAggregate)
}

func IsContinuousAggregate(ctx context.Context, db *pgxext.DataSource, view Relation) (bool, error) {
	_, err := GetContinuousAggregate(ctx, db, view)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, ErrNotContinuousAggregate) {
		return false, nil
	}
	return false, err
}

func ListJobs(ctx context.Context, db *pgxext.DataSource) ([]JobMetadata, error) {
	if ctx == nil {
		return nil, fmt.Errorf("timescaledb: list jobs: nil context")
	}
	if db == nil {
		return nil, fmt.Errorf("timescaledb: list jobs: nil DataSource")
	}
	rows, err := db.Query(ctx, `
SELECT job_id, application_name, schedule_interval, max_runtime, max_retries,
       retry_period, proc_schema, proc_name, owner, scheduled, fixed_schedule,
       config, next_start, initial_start, hypertable_schema, hypertable_name,
       check_schema, check_name
FROM timescaledb_information.jobs
ORDER BY job_id`)
	if err != nil {
		return nil, fmt.Errorf("timescaledb: list jobs: %w", err)
	}
	defer closeRows(rows)
	result := make([]JobMetadata, 0)
	for rows.Next() {
		item, err := scanJob(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("timescaledb: scan job: %w", err)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("timescaledb: list jobs: %w", err)
	}
	return result, nil
}

func GetJob(ctx context.Context, db *pgxext.DataSource, jobID int64) (*JobMetadata, error) {
	if jobID <= 0 {
		return nil, fmt.Errorf("timescaledb: job ID must be positive")
	}
	if ctx == nil || db == nil {
		return nil, fmt.Errorf("timescaledb: get job %d: nil context or DataSource", jobID)
	}
	rows, err := db.Query(ctx, `
SELECT job_id, application_name, schedule_interval, max_runtime, max_retries,
       retry_period, proc_schema, proc_name, owner, scheduled, fixed_schedule,
       config, next_start, initial_start, hypertable_schema, hypertable_name,
       check_schema, check_name
FROM timescaledb_information.jobs
WHERE job_id = $1`, jobID)
	if err != nil {
		return nil, fmt.Errorf("timescaledb: get job %d: %w", jobID, err)
	}
	defer closeRows(rows)
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("timescaledb: get job %d: %w", jobID, err)
		}
		return nil, nil
	}
	item, err := scanJob(rows.Scan)
	if err != nil {
		return nil, fmt.Errorf("timescaledb: get job %d: %w", jobID, err)
	}
	return &item, nil
}

type scanFunction func(...any) error

func scanJob(scan scanFunction) (JobMetadata, error) {
	var item JobMetadata
	var schedule, maxRuntime, retry pgtype.Interval
	var config []byte
	var nextRun, initialStart pgtype.Timestamptz
	var relationSchema, relationName, checkSchema, checkName pgtype.Text
	if err := scan(
		&item.ID, &item.ApplicationName, &schedule, &maxRuntime, &item.MaxRetries,
		&retry, &item.ProcedureSchema, &item.ProcedureName, &item.Owner, &item.Scheduled,
		&item.FixedSchedule, &config, &nextRun, &initialStart, &relationSchema,
		&relationName, &checkSchema, &checkName,
	); err != nil {
		return item, err
	}
	var err error
	if item.ScheduleInterval, err = intervalFromPG(schedule); err != nil {
		return item, err
	}
	if item.MaxRuntime, err = intervalFromPG(maxRuntime); err != nil {
		return item, err
	}
	if item.RetryPeriod, err = intervalFromPG(retry); err != nil {
		return item, err
	}
	item.Config = append(json.RawMessage(nil), config...)
	item.NextRun = timestamptzPointer(nextRun)
	item.InitialStart = timestamptzPointer(initialStart)
	if relationSchema.Valid && relationName.Valid {
		item.Relation = &Relation{Schema: relationSchema.String, Name: relationName.String}
	}
	if checkSchema.Valid {
		item.CheckSchema = checkSchema.String
	}
	if checkName.Valid {
		item.CheckName = checkName.String
	}
	return item, nil
}

func GetJobStats(ctx context.Context, db *pgxext.DataSource, jobID int64) (*JobStats, error) {
	if jobID <= 0 {
		return nil, fmt.Errorf("timescaledb: job ID must be positive")
	}
	if ctx == nil || db == nil {
		return nil, fmt.Errorf("timescaledb: get job stats %d: nil context or DataSource", jobID)
	}
	rows, err := db.Query(ctx, `
SELECT hypertable_schema, hypertable_name, job_id, last_run_started_at,
       last_successful_finish, last_run_status, job_status, last_run_duration,
       next_start, total_runs, total_successes, total_failures
FROM timescaledb_information.job_stats
WHERE job_id = $1`, jobID)
	if err != nil {
		return nil, fmt.Errorf("timescaledb: get job stats %d: %w", jobID, err)
	}
	defer closeRows(rows)
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("timescaledb: get job stats %d: %w", jobID, err)
		}
		return nil, nil
	}
	var item JobStats
	var schema, name, lastStatus, jobStatus pgtype.Text
	var started, finished, nextRun pgtype.Timestamptz
	var duration pgtype.Interval
	var totalRuns, totalSuccesses, totalFailures pgtype.Int8
	if err := rows.Scan(
		&schema, &name, &item.JobID, &started, &finished, &lastStatus, &jobStatus,
		&duration, &nextRun, &totalRuns, &totalSuccesses, &totalFailures,
	); err != nil {
		return nil, fmt.Errorf("timescaledb: get job stats %d: %w", jobID, err)
	}
	if schema.Valid && name.Valid {
		item.Relation = &Relation{Schema: schema.String, Name: name.String}
	}
	item.LastRunStartedAt = timestamptzPointer(started)
	item.LastSuccessfulFinish = timestamptzPointer(finished)
	item.NextRun = timestamptzPointer(nextRun)
	if lastStatus.Valid {
		item.LastRunStatus = lastStatus.String
	}
	if jobStatus.Valid {
		item.JobStatus = jobStatus.String
	}
	if duration.Valid {
		parsed, err := intervalFromPG(duration)
		if err != nil {
			return nil, fmt.Errorf("timescaledb: get job stats %d duration: %w", jobID, err)
		}
		if value, ok := parsed.Duration(); ok {
			item.LastRunDuration = &value
		}
	}
	// TimescaleDB 2.23 can expose NULL counters until a newly registered job
	// has run for the first time. Their observable count is zero in that state.
	if totalRuns.Valid {
		item.TotalRuns = totalRuns.Int64
	}
	if totalSuccesses.Valid {
		item.TotalSuccesses = totalSuccesses.Int64
	}
	if totalFailures.Valid {
		item.TotalFailures = totalFailures.Int64
	}
	return &item, nil
}

// GetPolicyJob returns a policy's job and latest statistics. A missing policy
// returns ErrPolicyNotFound.
func GetPolicyJob(ctx context.Context, db *pgxext.DataSource, relation Relation, kind PolicyKind) (*JobMetadata, *JobStats, error) {
	procedure, err := policyProcedure(kind)
	if err != nil {
		return nil, nil, fmt.Errorf("timescaledb: inspect policy job for %s: %w", relationContext(relation), err)
	}
	jobs, err := ListJobs(ctx, db)
	if err != nil {
		return nil, nil, fmt.Errorf("timescaledb: inspect %s policy job for %s: %w", kind, relationContext(relation), err)
	}
	wanted, found, err := resolveRelation(ctx, db, relation)
	if err != nil {
		return nil, nil, fmt.Errorf("timescaledb: inspect %s policy job for %s: %w", kind, relationContext(relation), err)
	}
	if !found {
		return nil, nil, fmt.Errorf("timescaledb: inspect %s policy job for %s: %w", kind, relationContext(relation), ErrPolicyNotFound)
	}
	for index := range jobs {
		if jobs[index].ProcedureName != procedure || jobs[index].Relation == nil {
			continue
		}
		if *jobs[index].Relation != wanted {
			continue
		}
		stats, err := GetJobStats(ctx, db, jobs[index].ID)
		if err != nil {
			return nil, nil, err
		}
		return &jobs[index], stats, nil
	}
	return nil, nil, fmt.Errorf("timescaledb: inspect %s policy job for %s: %w", kind, relationContext(relation), ErrPolicyNotFound)
}

// resolveRelation applies the connection's search_path when Schema is empty.
// pg_catalog is used only for PostgreSQL name resolution; Timescale metadata
// continues to come from documented timescaledb_information views.
func resolveRelation(ctx context.Context, db *pgxext.DataSource, relation Relation) (Relation, bool, error) {
	if err := relation.validate(); err != nil {
		return Relation{}, false, err
	}
	if relation.Schema != "" {
		return relation, true, nil
	}
	regclass, _ := relation.regclassText()
	rows, err := db.Query(ctx, `
SELECT n.nspname, c.relname
FROM pg_catalog.pg_class c
JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
WHERE c.oid = to_regclass($1)`, regclass)
	if err != nil {
		return Relation{}, false, fmt.Errorf("resolve relation %s: %w", relationContext(relation), err)
	}
	defer closeRows(rows)
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return Relation{}, false, fmt.Errorf("resolve relation %s: %w", relationContext(relation), err)
		}
		return Relation{}, false, nil
	}
	var resolved Relation
	if err := rows.Scan(&resolved.Schema, &resolved.Name); err != nil {
		return Relation{}, false, fmt.Errorf("resolve relation %s: %w", relationContext(relation), err)
	}
	return resolved, true, nil
}

func policyProcedure(kind PolicyKind) (string, error) {
	switch kind {
	case RefreshPolicyKind:
		return "policy_refresh_continuous_aggregate", nil
	case RetentionPolicyKind:
		return "policy_retention", nil
	case ColumnstorePolicyKind:
		return "policy_compression", nil
	default:
		return "", fmt.Errorf("timescaledb: unsupported policy kind %q", kind)
	}
}

func timestamptzPointer(value pgtype.Timestamptz) *time.Time {
	if !value.Valid || value.InfinityModifier != pgtype.Finite {
		return nil
	}
	result := value.Time
	return &result
}

func int64Pointer(value pgtype.Int8) *int64 {
	if !value.Valid {
		return nil
	}
	result := value.Int64
	return &result
}
