package timescaledb

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/0x626f/pgxext"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// RefreshPolicy configures a continuous aggregate refresh job. Open-ended
// start offsets use OpenEndedInterval. An open-ended end includes the current,
// potentially incomplete bucket and therefore also requires
// AllowIncompleteBucket.
type RefreshPolicy struct {
	StartOffset            Interval
	EndOffset              Interval
	ScheduleInterval       Interval
	InitialStart           *time.Time
	Timezone               string
	IfNotExists            bool
	AllowIncompleteBucket  bool
	IncludeTieredData      *bool
	BucketsPerBatch        int
	MaxBatchesPerExecution int
	RefreshNewestFirst     bool
}

// RefreshPolicyMetadata includes the configured policy and its Timescale job.
type RefreshPolicyMetadata struct {
	Policy RefreshPolicy
	Job    JobMetadata
	Stats  *JobStats
}

func (p RefreshPolicy) Validate() error {
	if !p.StartOffset.IsOpenEnded() {
		if err := p.StartOffset.Validate(); err != nil {
			return fmt.Errorf("timescaledb: invalid refresh start_offset: %w", err)
		}
	}
	if !p.EndOffset.IsOpenEnded() {
		if err := p.EndOffset.Validate(); err != nil {
			return fmt.Errorf("timescaledb: invalid refresh end_offset: %w", err)
		}
	} else if !p.AllowIncompleteBucket {
		return fmt.Errorf("timescaledb: open-ended end_offset requires AllowIncompleteBucket")
	}
	if !p.StartOffset.IsOpenEnded() && !p.EndOffset.IsOpenEnded() {
		comparison, err := compareIntervals(p.StartOffset, p.EndOffset)
		if err != nil {
			return fmt.Errorf("timescaledb: compare refresh offsets: %w", err)
		}
		if comparison <= 0 {
			return fmt.Errorf("timescaledb: refresh start_offset must be greater than end_offset")
		}
	}
	if err := p.ScheduleInterval.Validate(); err != nil {
		return fmt.Errorf("timescaledb: invalid refresh schedule_interval: %w", err)
	}
	if err := validateTimezone(p.Timezone); err != nil {
		return err
	}
	if p.BucketsPerBatch < 0 {
		return fmt.Errorf("timescaledb: buckets_per_batch must be non-negative")
	}
	if p.MaxBatchesPerExecution < 0 {
		return fmt.Errorf("timescaledb: max_batches_per_execution must be non-negative")
	}
	if p.BucketsPerBatch == 0 && p.MaxBatchesPerExecution != 0 {
		return fmt.Errorf("timescaledb: max_batches_per_execution requires buckets_per_batch")
	}
	return nil
}

func validateTimezone(value string) error {
	if value == "" {
		return nil
	}
	// Local is a Go process pseudo-location, not a portable PostgreSQL zone
	// name. Requiring a concrete name keeps policy semantics reproducible.
	if value == "Local" {
		return fmt.Errorf("timescaledb: invalid timezone %q: use a concrete IANA timezone", value)
	}
	if _, err := time.LoadLocation(value); err != nil {
		return fmt.Errorf("timescaledb: invalid timezone %q: %w", value, err)
	}
	return nil
}

// PlanRefreshPolicy derives a fixed-width UTC policy. A zero schedule uses the
// bucket width. Calendar and timezone-aligned buckets require explicit offsets.
func PlanRefreshPolicy(bucketWidth, lateArrivalWindow, schedule time.Duration) (RefreshPolicy, error) {
	if err := FixedInterval(bucketWidth).Validate(); err != nil {
		return RefreshPolicy{}, fmt.Errorf("timescaledb: plan refresh policy bucket width: %w", err)
	}
	if lateArrivalWindow < 0 {
		return RefreshPolicy{}, fmt.Errorf("timescaledb: late-arrival window must be non-negative")
	}
	if lateArrivalWindow%time.Microsecond != 0 {
		return RefreshPolicy{}, fmt.Errorf("timescaledb: late-arrival window must use PostgreSQL microsecond precision")
	}
	if schedule < 0 {
		return RefreshPolicy{}, fmt.Errorf("timescaledb: refresh schedule must not be negative")
	}
	if schedule == 0 {
		schedule = bucketWidth
	}
	if err := FixedInterval(schedule).Validate(); err != nil {
		return RefreshPolicy{}, fmt.Errorf("timescaledb: plan refresh schedule: %w", err)
	}
	if bucketWidth > time.Duration(math.MaxInt64/2) {
		return RefreshPolicy{}, fmt.Errorf("timescaledb: bucket width overflows derived refresh window")
	}
	twoBuckets := 2 * bucketWidth
	if lateArrivalWindow > time.Duration(math.MaxInt64)-bucketWidth {
		return RefreshPolicy{}, fmt.Errorf("timescaledb: late-arrival window overflows derived refresh window")
	}
	start := lateArrivalWindow + bucketWidth
	if twoBuckets > start {
		start = twoBuckets
	}
	return RefreshPolicy{
		StartOffset:        FixedInterval(start),
		EndOffset:          FixedInterval(bucketWidth),
		ScheduleInterval:   FixedInterval(schedule),
		RefreshNewestFirst: true,
	}, nil
}

// RefreshPoliciesEqual compares persisted semantics. IfNotExists and
// AllowIncompleteBucket are invocation/validation controls and are ignored.
func RefreshPoliciesEqual(left, right RefreshPolicy) bool {
	return intervalsEqual(left.StartOffset, right.StartOffset) &&
		intervalsEqual(left.EndOffset, right.EndOffset) &&
		intervalsEqual(left.ScheduleInterval, right.ScheduleInterval) &&
		timesEqual(left.InitialStart, right.InitialStart) &&
		left.Timezone == right.Timezone &&
		boolPointersEqual(left.IncludeTieredData, right.IncludeTieredData) &&
		left.BucketsPerBatch == right.BucketsPerBatch &&
		left.MaxBatchesPerExecution == right.MaxBatchesPerExecution &&
		left.RefreshNewestFirst == right.RefreshNewestFirst
}

func timesEqual(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.UTC().Truncate(time.Microsecond).Equal(right.UTC().Truncate(time.Microsecond))
}

func DecideRefreshPolicy(current *RefreshPolicy, desired RefreshPolicy, reconcile bool) (PolicyDecision, error) {
	if err := desired.Validate(); err != nil {
		return "", err
	}
	if current == nil {
		return PolicyDecisionCreate, nil
	}
	if RefreshPoliciesEqual(*current, desired) {
		return PolicyDecisionNoop, nil
	}
	if reconcile {
		return PolicyDecisionReplace, nil
	}
	return PolicyDecisionConflict, nil
}

func AddRefreshPolicy(ctx context.Context, db *pgxext.DataSource, view Relation, policy RefreshPolicy) (int64, error) {
	if err := policy.Validate(); err != nil {
		return 0, fmt.Errorf("timescaledb: add refresh policy for %s: %w", relationContext(view), err)
	}
	if err := view.validate(); err != nil {
		return 0, fmt.Errorf("timescaledb: add refresh policy for %s: %w", relationContext(view), err)
	}
	if ctx == nil || db == nil {
		return 0, fmt.Errorf("timescaledb: add refresh policy for %s: nil context or DataSource", relationContext(view))
	}
	return addRefreshPolicy(ctx, db.Query, view, policy)
}

func addRefreshPolicy(ctx context.Context, query func(context.Context, string, ...any) (pgx.Rows, error), view Relation, policy RefreshPolicy) (int64, error) {
	regclass, _ := view.regclassText()
	start, _ := policy.StartOffset.nullablePGValue()
	end, _ := policy.EndOffset.nullablePGValue()
	schedule, _ := policy.ScheduleInterval.pgValue()
	var initial any
	if policy.InitialStart != nil {
		initial = policy.InitialStart.UTC()
	}
	var timezone any
	if policy.Timezone != "" {
		timezone = policy.Timezone
	}
	var includeTieredData any
	if policy.IncludeTieredData != nil {
		includeTieredData = *policy.IncludeTieredData
	}
	rows, err := query(ctx, `
SELECT add_continuous_aggregate_policy(
  $1::regclass,
  start_offset => $2::interval,
  end_offset => $3::interval,
  schedule_interval => $4::interval,
  if_not_exists => $5::boolean,
  initial_start => $6::timestamptz,
  timezone => $7::text,
  include_tiered_data => $8::boolean,
  buckets_per_batch => $9::integer,
  max_batches_per_execution => $10::integer,
  refresh_newest_first => $11::boolean
)`, regclass, start, end, schedule, policy.IfNotExists, initial, timezone,
		includeTieredData, policy.BucketsPerBatch, policy.MaxBatchesPerExecution, policy.RefreshNewestFirst)
	if err != nil {
		return 0, fmt.Errorf("timescaledb: add refresh policy for %s: %w", relationContext(view), err)
	}
	defer closeRows(rows)
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return 0, fmt.Errorf("timescaledb: add refresh policy for %s: %w", relationContext(view), err)
		}
		return 0, fmt.Errorf("timescaledb: add refresh policy for %s: no job ID returned", relationContext(view))
	}
	var jobID int64
	if err := rows.Scan(&jobID); err != nil {
		return 0, fmt.Errorf("timescaledb: add refresh policy for %s: %w", relationContext(view), err)
	}
	return jobID, nil
}

// InspectRefreshPolicy reads public job metadata. TimescaleDB 2.20 stores job
// timezone only in _timescaledb_config.bgw_job; the isolated LEFT JOIN below is
// the sole internal-catalog compatibility dependency in this package.
func InspectRefreshPolicy(ctx context.Context, db *pgxext.DataSource, view Relation) (*RefreshPolicyMetadata, error) {
	if err := view.validate(); err != nil {
		return nil, fmt.Errorf("timescaledb: inspect refresh policy for %s: %w", relationContext(view), err)
	}
	if ctx == nil || db == nil {
		return nil, fmt.Errorf("timescaledb: inspect refresh policy for %s: nil context or DataSource", relationContext(view))
	}
	regclass, _ := view.regclassText()
	rows, err := db.Query(ctx, `
SELECT j.job_id, j.schedule_interval, j.initial_start, internal_job.timezone,
       NULLIF(j.config->>'start_offset', '')::interval,
       NULLIF(j.config->>'end_offset', '')::interval,
       (j.config->>'include_tiered_data')::boolean,
       COALESCE((j.config->>'buckets_per_batch')::integer, 0),
       COALESCE((j.config->>'max_batches_per_execution')::integer, 10),
       COALESCE((j.config->>'refresh_newest_first')::boolean, TRUE)
FROM timescaledb_information.jobs j
LEFT JOIN _timescaledb_config.bgw_job internal_job ON internal_job.id = j.job_id
WHERE j.proc_name = 'policy_refresh_continuous_aggregate'
  AND format('%I.%I', j.hypertable_schema, j.hypertable_name)::regclass = $1::regclass
ORDER BY j.job_id
LIMIT 1`, regclass)
	if err != nil {
		return nil, fmt.Errorf("timescaledb: inspect refresh policy for %s: %w", relationContext(view), err)
	}
	defer closeRows(rows)
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("timescaledb: inspect refresh policy for %s: %w", relationContext(view), err)
		}
		return nil, fmt.Errorf("timescaledb: inspect refresh policy for %s: %w", relationContext(view), ErrPolicyNotFound)
	}
	var jobID int64
	var schedule, start, end pgtype.Interval
	var initial pgtype.Timestamptz
	var timezone pgtype.Text
	var includeTieredData pgtype.Bool
	var buckets, maxBatches int
	var newestFirst bool
	if err := rows.Scan(&jobID, &schedule, &initial, &timezone, &start, &end, &includeTieredData, &buckets, &maxBatches, &newestFirst); err != nil {
		return nil, fmt.Errorf("timescaledb: inspect refresh policy for %s: %w", relationContext(view), err)
	}
	policy := RefreshPolicy{
		InitialStart:           timestamptzPointer(initial),
		BucketsPerBatch:        buckets,
		MaxBatchesPerExecution: maxBatches,
		RefreshNewestFirst:     newestFirst,
	}
	if timezone.Valid {
		policy.Timezone = timezone.String
	}
	if includeTieredData.Valid {
		value := includeTieredData.Bool
		policy.IncludeTieredData = &value
	}
	if policy.ScheduleInterval, err = intervalFromPG(schedule); err != nil {
		return nil, fmt.Errorf("timescaledb: inspect refresh policy for %s: %w", relationContext(view), err)
	}
	if policy.StartOffset, err = intervalFromPG(start); err != nil {
		return nil, fmt.Errorf("timescaledb: inspect refresh policy for %s: %w", relationContext(view), err)
	}
	if policy.EndOffset, err = intervalFromPG(end); err != nil {
		return nil, fmt.Errorf("timescaledb: inspect refresh policy for %s: %w", relationContext(view), err)
	}
	policy.AllowIncompleteBucket = policy.EndOffset.IsOpenEnded()
	job, err := GetJob(ctx, db, jobID)
	if err != nil {
		return nil, err
	}
	if job == nil {
		return nil, fmt.Errorf("timescaledb: inspect refresh policy for %s: job %d disappeared", relationContext(view), jobID)
	}
	stats, err := GetJobStats(ctx, db, jobID)
	if err != nil {
		return nil, err
	}
	return &RefreshPolicyMetadata{Policy: policy, Job: *job, Stats: stats}, nil
}

func EnsureRefreshPolicy(ctx context.Context, db *pgxext.DataSource, view Relation, desired RefreshPolicy) (PolicyAction, error) {
	return ensureRefreshPolicy(ctx, db, view, desired, false)
}

func ReconcileRefreshPolicy(ctx context.Context, db *pgxext.DataSource, view Relation, desired RefreshPolicy) (PolicyAction, error) {
	return ensureRefreshPolicy(ctx, db, view, desired, true)
}

func ensureRefreshPolicy(ctx context.Context, db *pgxext.DataSource, view Relation, desired RefreshPolicy, reconcile bool) (PolicyAction, error) {
	if err := desired.Validate(); err != nil {
		return "", fmt.Errorf("timescaledb: reconcile refresh policy for %s: %w", relationContext(view), err)
	}
	if err := view.validate(); err != nil {
		return "", fmt.Errorf("timescaledb: reconcile refresh policy for %s: %w", relationContext(view), err)
	}
	if ctx == nil || db == nil {
		return "", fmt.Errorf("timescaledb: reconcile refresh policy for %s: nil context or DataSource", relationContext(view))
	}
	metadata, err := InspectRefreshPolicy(ctx, db, view)
	var current *RefreshPolicy
	if err == nil {
		current = &metadata.Policy
	} else if !errors.Is(err, ErrPolicyNotFound) {
		return "", err
	}
	decision, err := DecideRefreshPolicy(current, desired, reconcile)
	if err != nil {
		return "", err
	}
	switch decision {
	case PolicyDecisionCreate:
		if _, err := addRefreshPolicy(ctx, db.Query, view, desired); err != nil {
			return "", err
		}
		return PolicyCreated, nil
	case PolicyDecisionNoop:
		return PolicyUnchanged, nil
	case PolicyDecisionConflict:
		return PolicyUnchanged, fmt.Errorf("timescaledb: ensure refresh policy for %s: %w", relationContext(view), ErrPolicyDrift)
	case PolicyDecisionReplace:
		return ReplaceRefreshPolicy(ctx, db, view, desired)
	default:
		return "", fmt.Errorf("timescaledb: unsupported refresh policy decision %q", decision)
	}
}

func ReplaceRefreshPolicy(ctx context.Context, db *pgxext.DataSource, view Relation, desired RefreshPolicy) (PolicyAction, error) {
	if err := desired.Validate(); err != nil {
		return "", fmt.Errorf("timescaledb: replace refresh policy for %s: %w", relationContext(view), err)
	}
	if err := view.validate(); err != nil {
		return "", fmt.Errorf("timescaledb: replace refresh policy for %s: %w", relationContext(view), err)
	}
	if ctx == nil || db == nil {
		return "", fmt.Errorf("timescaledb: replace refresh policy for %s: nil context or DataSource", relationContext(view))
	}
	err := withTransaction(ctx, db, "replace refresh policy", func(tx pgx.Tx) error {
		if err := removeRefreshPolicy(ctx, tx.Exec, view, true); err != nil {
			return err
		}
		_, err := addRefreshPolicy(ctx, tx.Query, view, desired)
		return err
	})
	if err != nil {
		return "", fmt.Errorf("timescaledb: replace refresh policy for %s: %w", relationContext(view), err)
	}
	return PolicyReplaced, nil
}

func RemoveRefreshPolicy(ctx context.Context, db *pgxext.DataSource, view Relation, ifExists bool) error {
	if err := view.validate(); err != nil {
		return fmt.Errorf("timescaledb: remove refresh policy for %s: %w", relationContext(view), err)
	}
	if ctx == nil || db == nil {
		return fmt.Errorf("timescaledb: remove refresh policy for %s: nil context or DataSource", relationContext(view))
	}
	return removeRefreshPolicy(ctx, db.Exec, view, ifExists)
}

func removeRefreshPolicy(ctx context.Context, exec func(context.Context, string, ...any) (pgconn.CommandTag, error), view Relation, ifExists bool) error {
	regclass, _ := view.regclassText()
	if _, err := exec(ctx, `SELECT remove_continuous_aggregate_policy($1::regclass, if_exists => $2::boolean)`, regclass, ifExists); err != nil {
		return fmt.Errorf("timescaledb: remove refresh policy for %s: %w", relationContext(view), err)
	}
	return nil
}

// RefreshContinuousAggregate manually refreshes complete buckets wholly inside
// the explicit half-open range [start,end). It never uses PostgreSQL REFRESH
// MATERIALIZED VIEW.
func RefreshContinuousAggregate(ctx context.Context, db *pgxext.DataSource, view Relation, start, end time.Time, force bool) error {
	if err := view.validate(); err != nil {
		return fmt.Errorf("timescaledb: refresh continuous aggregate %s: %w", relationContext(view), err)
	}
	if !start.Before(end) {
		return fmt.Errorf("timescaledb: refresh continuous aggregate %s: start must be before end", relationContext(view))
	}
	if ctx == nil || db == nil {
		return fmt.Errorf("timescaledb: refresh continuous aggregate %s: nil context or DataSource", relationContext(view))
	}
	regclass, _ := view.regclassText()
	if _, err := db.Exec(ctx, `CALL refresh_continuous_aggregate($1::regclass, $2::timestamptz, $3::timestamptz, force => $4::boolean)`, regclass, start, end, force); err != nil {
		return fmt.Errorf("timescaledb: refresh continuous aggregate %s over [%s,%s): %w", relationContext(view), start.Format(time.RFC3339Nano), end.Format(time.RFC3339Nano), err)
	}
	return nil
}

// TimeRange is a half-open TIMESTAMPTZ range.
type TimeRange struct {
	Start time.Time
	End   time.Time
}

// PlanRefreshBatches splits a large backfill into bounded half-open ranges.
// It errors instead of allocating more than maxBatches ranges. The caller must
// supply start and end on continuous-aggregate bucket boundaries and choose a
// batch width that preserves those boundaries; otherwise Timescale's
// full-bucket refresh semantics can leave a bucket crossing two batches
// unrefreshed. Calendar and timezone boundary calculation is application-owned.
func PlanRefreshBatches(start, end time.Time, batchWidth time.Duration, maxBatches int) ([]TimeRange, error) {
	if !start.Before(end) {
		return nil, fmt.Errorf("timescaledb: batch range start must be before end")
	}
	if err := FixedInterval(batchWidth).Validate(); err != nil {
		return nil, fmt.Errorf("timescaledb: invalid refresh batch width: %w", err)
	}
	if maxBatches <= 0 {
		return nil, fmt.Errorf("timescaledb: maxBatches must be positive")
	}
	result := make([]TimeRange, 0)
	for cursor := start; cursor.Before(end); {
		if len(result) == maxBatches {
			return nil, fmt.Errorf("timescaledb: refresh plan exceeds maxBatches %d", maxBatches)
		}
		next := cursor.Add(batchWidth)
		if next.Before(cursor) || next.After(end) {
			next = end
		}
		result = append(result, TimeRange{Start: cursor, End: next})
		cursor = next
	}
	return result, nil
}
