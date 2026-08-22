package timescaledb

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/0x626f/pgxext"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// RetentionPolicy controls destructive chunk deletion. DropAfter is based on
// the time values in chunks, not chunk creation time.
type RetentionPolicy struct {
	DropAfter        Interval
	ScheduleInterval Interval
	InitialStart     *time.Time
	Timezone         string
	IfNotExists      bool
}

type RetentionPolicyMetadata struct {
	Policy RetentionPolicy
	Job    JobMetadata
	Stats  *JobStats
}

// NewRetentionPolicy uses a deterministic 24-hour schedule instead of relying
// on a server default.
func NewRetentionPolicy(dropAfter Interval) RetentionPolicy {
	return RetentionPolicy{DropAfter: dropAfter, ScheduleInterval: FixedInterval(24 * time.Hour)}
}

func normalizeRetentionPolicy(policy RetentionPolicy) RetentionPolicy {
	if policy.ScheduleInterval.IsOpenEnded() {
		policy.ScheduleInterval = FixedInterval(24 * time.Hour)
	}
	return policy
}

func (p RetentionPolicy) Validate() error {
	p = normalizeRetentionPolicy(p)
	if err := p.DropAfter.Validate(); err != nil {
		return fmt.Errorf("timescaledb: invalid retention drop_after: %w", err)
	}
	if err := p.ScheduleInterval.Validate(); err != nil {
		return fmt.Errorf("timescaledb: invalid retention schedule_interval: %w", err)
	}
	return validateTimezone(p.Timezone)
}

func RetentionPoliciesEqual(left, right RetentionPolicy) bool {
	left, right = normalizeRetentionPolicy(left), normalizeRetentionPolicy(right)
	return intervalsEqual(left.DropAfter, right.DropAfter) &&
		intervalsEqual(left.ScheduleInterval, right.ScheduleInterval) &&
		timesEqual(left.InitialStart, right.InitialStart) &&
		left.Timezone == right.Timezone
}

func DecideRetentionPolicy(current *RetentionPolicy, desired RetentionPolicy, reconcile bool) (PolicyDecision, error) {
	if err := desired.Validate(); err != nil {
		return "", err
	}
	if current == nil {
		return PolicyDecisionCreate, nil
	}
	if RetentionPoliciesEqual(*current, desired) {
		return PolicyDecisionNoop, nil
	}
	if reconcile {
		return PolicyDecisionReplace, nil
	}
	return PolicyDecisionConflict, nil
}

func AddRetentionPolicy(ctx context.Context, db *pgxext.DataSource, relation Relation, policy RetentionPolicy) (int64, error) {
	policy = normalizeRetentionPolicy(policy)
	if err := policy.Validate(); err != nil {
		return 0, fmt.Errorf("timescaledb: add retention policy for %s: %w", relationContext(relation), err)
	}
	if err := relation.validate(); err != nil {
		return 0, fmt.Errorf("timescaledb: add retention policy for %s: %w", relationContext(relation), err)
	}
	if ctx == nil || db == nil {
		return 0, fmt.Errorf("timescaledb: add retention policy for %s: nil context or DataSource", relationContext(relation))
	}
	return addRetentionPolicy(ctx, db.Query, relation, policy)
}

func addRetentionPolicy(ctx context.Context, query func(context.Context, string, ...any) (pgx.Rows, error), relation Relation, policy RetentionPolicy) (int64, error) {
	regclass, _ := relation.regclassText()
	dropAfter, _ := policy.DropAfter.pgValue()
	schedule, _ := policy.ScheduleInterval.pgValue()
	var initial, timezone any
	if policy.InitialStart != nil {
		initial = policy.InitialStart.UTC()
	}
	if policy.Timezone != "" {
		timezone = policy.Timezone
	}
	rows, err := query(ctx, `
SELECT add_retention_policy(
  $1::regclass,
  drop_after => $2::interval,
  if_not_exists => $3::boolean,
  schedule_interval => $4::interval,
  initial_start => $5::timestamptz,
  timezone => $6::text
)`, regclass, dropAfter, policy.IfNotExists, schedule, initial, timezone)
	if err != nil {
		return 0, fmt.Errorf("timescaledb: add destructive retention policy for %s: %w", relationContext(relation), err)
	}
	defer closeRows(rows)
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return 0, fmt.Errorf("timescaledb: add destructive retention policy for %s: %w", relationContext(relation), err)
		}
		return 0, fmt.Errorf("timescaledb: add destructive retention policy for %s: no job ID returned", relationContext(relation))
	}
	var jobID int64
	if err := rows.Scan(&jobID); err != nil {
		return 0, fmt.Errorf("timescaledb: add destructive retention policy for %s: %w", relationContext(relation), err)
	}
	return jobID, nil
}

func InspectRetentionPolicy(ctx context.Context, db *pgxext.DataSource, relation Relation) (*RetentionPolicyMetadata, error) {
	if err := relation.validate(); err != nil {
		return nil, fmt.Errorf("timescaledb: inspect retention policy for %s: %w", relationContext(relation), err)
	}
	if ctx == nil || db == nil {
		return nil, fmt.Errorf("timescaledb: inspect retention policy for %s: nil context or DataSource", relationContext(relation))
	}
	regclass, _ := relation.regclassText()
	rows, err := db.Query(ctx, `
SELECT j.job_id, j.schedule_interval, j.initial_start, internal_job.timezone,
       NULLIF(j.config->>'drop_after', '')::interval
FROM timescaledb_information.jobs j
LEFT JOIN _timescaledb_config.bgw_job internal_job ON internal_job.id = j.job_id
WHERE j.proc_name = 'policy_retention'
  AND format('%I.%I', j.hypertable_schema, j.hypertable_name)::regclass = $1::regclass
ORDER BY j.job_id
LIMIT 1`, regclass)
	if err != nil {
		return nil, fmt.Errorf("timescaledb: inspect retention policy for %s: %w", relationContext(relation), err)
	}
	defer closeRows(rows)
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("timescaledb: inspect retention policy for %s: %w", relationContext(relation), err)
		}
		return nil, fmt.Errorf("timescaledb: inspect retention policy for %s: %w", relationContext(relation), ErrPolicyNotFound)
	}
	var jobID int64
	var schedule, dropAfter pgtype.Interval
	var initial pgtype.Timestamptz
	var timezone pgtype.Text
	if err := rows.Scan(&jobID, &schedule, &initial, &timezone, &dropAfter); err != nil {
		return nil, fmt.Errorf("timescaledb: inspect retention policy for %s: %w", relationContext(relation), err)
	}
	policy := RetentionPolicy{InitialStart: timestamptzPointer(initial)}
	if timezone.Valid {
		policy.Timezone = timezone.String
	}
	if policy.ScheduleInterval, err = intervalFromPG(schedule); err != nil {
		return nil, fmt.Errorf("timescaledb: inspect retention policy for %s: %w", relationContext(relation), err)
	}
	if policy.DropAfter, err = intervalFromPG(dropAfter); err != nil {
		return nil, fmt.Errorf("timescaledb: inspect retention policy for %s: %w", relationContext(relation), err)
	}
	job, err := GetJob(ctx, db, jobID)
	if err != nil {
		return nil, err
	}
	if job == nil {
		return nil, fmt.Errorf("timescaledb: inspect retention policy for %s: job %d disappeared", relationContext(relation), jobID)
	}
	stats, err := GetJobStats(ctx, db, jobID)
	if err != nil {
		return nil, err
	}
	return &RetentionPolicyMetadata{Policy: policy, Job: *job, Stats: stats}, nil
}

func EnsureRetentionPolicy(ctx context.Context, db *pgxext.DataSource, relation Relation, desired RetentionPolicy) (PolicyAction, error) {
	return ensureRetentionPolicy(ctx, db, relation, desired, false)
}

func ReconcileRetentionPolicy(ctx context.Context, db *pgxext.DataSource, relation Relation, desired RetentionPolicy) (PolicyAction, error) {
	return ensureRetentionPolicy(ctx, db, relation, desired, true)
}

func ensureRetentionPolicy(ctx context.Context, db *pgxext.DataSource, relation Relation, desired RetentionPolicy, reconcile bool) (PolicyAction, error) {
	desired = normalizeRetentionPolicy(desired)
	if err := desired.Validate(); err != nil {
		return "", fmt.Errorf("timescaledb: reconcile retention policy for %s: %w", relationContext(relation), err)
	}
	if err := relation.validate(); err != nil {
		return "", fmt.Errorf("timescaledb: reconcile retention policy for %s: %w", relationContext(relation), err)
	}
	if ctx == nil || db == nil {
		return "", fmt.Errorf("timescaledb: reconcile retention policy for %s: nil context or DataSource", relationContext(relation))
	}
	metadata, err := InspectRetentionPolicy(ctx, db, relation)
	var current *RetentionPolicy
	if err == nil {
		current = &metadata.Policy
	} else if !errors.Is(err, ErrPolicyNotFound) {
		return "", err
	}
	decision, err := DecideRetentionPolicy(current, desired, reconcile)
	if err != nil {
		return "", err
	}
	switch decision {
	case PolicyDecisionCreate:
		if _, err := addRetentionPolicy(ctx, db.Query, relation, desired); err != nil {
			return "", err
		}
		return PolicyCreated, nil
	case PolicyDecisionNoop:
		return PolicyUnchanged, nil
	case PolicyDecisionConflict:
		return PolicyUnchanged, fmt.Errorf("timescaledb: ensure retention policy for %s: %w", relationContext(relation), ErrPolicyDrift)
	case PolicyDecisionReplace:
		return ReplaceRetentionPolicy(ctx, db, relation, desired)
	default:
		return "", fmt.Errorf("timescaledb: unsupported retention policy decision %q", decision)
	}
}

func ReplaceRetentionPolicy(ctx context.Context, db *pgxext.DataSource, relation Relation, desired RetentionPolicy) (PolicyAction, error) {
	desired = normalizeRetentionPolicy(desired)
	if err := desired.Validate(); err != nil {
		return "", fmt.Errorf("timescaledb: replace retention policy for %s: %w", relationContext(relation), err)
	}
	if err := relation.validate(); err != nil {
		return "", fmt.Errorf("timescaledb: replace retention policy for %s: %w", relationContext(relation), err)
	}
	if ctx == nil || db == nil {
		return "", fmt.Errorf("timescaledb: replace retention policy for %s: nil context or DataSource", relationContext(relation))
	}
	err := withTransaction(ctx, db, "replace retention policy", func(tx pgx.Tx) error {
		if err := removeRetentionPolicy(ctx, tx.Exec, relation, true); err != nil {
			return err
		}
		_, err := addRetentionPolicy(ctx, tx.Query, relation, desired)
		return err
	})
	if err != nil {
		return "", fmt.Errorf("timescaledb: replace retention policy for %s: %w", relationContext(relation), err)
	}
	return PolicyReplaced, nil
}

func RemoveRetentionPolicy(ctx context.Context, db *pgxext.DataSource, relation Relation, ifExists bool) error {
	if err := relation.validate(); err != nil {
		return fmt.Errorf("timescaledb: remove retention policy for %s: %w", relationContext(relation), err)
	}
	if ctx == nil || db == nil {
		return fmt.Errorf("timescaledb: remove retention policy for %s: nil context or DataSource", relationContext(relation))
	}
	return removeRetentionPolicy(ctx, db.Exec, relation, ifExists)
}

func removeRetentionPolicy(ctx context.Context, exec func(context.Context, string, ...any) (pgconn.CommandTag, error), relation Relation, ifExists bool) error {
	regclass, _ := relation.regclassText()
	if _, err := exec(ctx, `SELECT remove_retention_policy($1::regclass, if_exists => $2::boolean)`, regclass, ifExists); err != nil {
		return fmt.Errorf("timescaledb: remove destructive retention policy for %s: %w", relationContext(relation), err)
	}
	return nil
}
