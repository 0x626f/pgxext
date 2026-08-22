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

// ColumnstorePolicy controls automatic rowstore-to-columnstore conversion.
type ColumnstorePolicy struct {
	After            Interval
	ScheduleInterval Interval
	InitialStart     *time.Time
	Timezone         string
	IfNotExists      bool
}

type ColumnstorePolicyMetadata struct {
	Policy ColumnstorePolicy
	Job    JobMetadata
	Stats  *JobStats
}

// NewColumnstorePolicy uses a deterministic 12-hour schedule.
func NewColumnstorePolicy(after Interval) ColumnstorePolicy {
	return ColumnstorePolicy{After: after, ScheduleInterval: FixedInterval(12 * time.Hour)}
}

func normalizeColumnstorePolicy(policy ColumnstorePolicy) ColumnstorePolicy {
	if policy.ScheduleInterval.IsOpenEnded() {
		policy.ScheduleInterval = FixedInterval(12 * time.Hour)
	}
	return policy
}

func (p ColumnstorePolicy) Validate() error {
	p = normalizeColumnstorePolicy(p)
	if err := p.After.Validate(); err != nil {
		return fmt.Errorf("timescaledb: invalid columnstore after: %w", err)
	}
	if err := p.ScheduleInterval.Validate(); err != nil {
		return fmt.Errorf("timescaledb: invalid columnstore schedule_interval: %w", err)
	}
	return validateTimezone(p.Timezone)
}

func ColumnstorePoliciesEqual(left, right ColumnstorePolicy) bool {
	left, right = normalizeColumnstorePolicy(left), normalizeColumnstorePolicy(right)
	return intervalsEqual(left.After, right.After) &&
		intervalsEqual(left.ScheduleInterval, right.ScheduleInterval) &&
		timesEqual(left.InitialStart, right.InitialStart) &&
		left.Timezone == right.Timezone
}

func boolPointersEqual(left, right *bool) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func DecideColumnstorePolicy(current *ColumnstorePolicy, desired ColumnstorePolicy, reconcile bool) (PolicyDecision, error) {
	if err := desired.Validate(); err != nil {
		return "", err
	}
	if current == nil {
		return PolicyDecisionCreate, nil
	}
	if ColumnstorePoliciesEqual(*current, desired) {
		return PolicyDecisionNoop, nil
	}
	if reconcile {
		return PolicyDecisionReplace, nil
	}
	return PolicyDecisionConflict, nil
}

func AddColumnstorePolicy(ctx context.Context, db *pgxext.DataSource, relation Relation, policy ColumnstorePolicy) error {
	policy = normalizeColumnstorePolicy(policy)
	if err := policy.Validate(); err != nil {
		return fmt.Errorf("timescaledb: add columnstore policy for %s: %w", relationContext(relation), err)
	}
	if err := relation.validate(); err != nil {
		return fmt.Errorf("timescaledb: add columnstore policy for %s: %w", relationContext(relation), err)
	}
	if ctx == nil || db == nil {
		return fmt.Errorf("timescaledb: add columnstore policy for %s: nil context or DataSource", relationContext(relation))
	}
	if _, err := requireCapability(ctx, db, "columnstore policy"); err != nil {
		return fmt.Errorf("timescaledb: add columnstore policy for %s: %w", relationContext(relation), err)
	}
	return addColumnstorePolicy(ctx, db.Exec, relation, policy)
}

func addColumnstorePolicy(ctx context.Context, exec func(context.Context, string, ...any) (pgconn.CommandTag, error), relation Relation, policy ColumnstorePolicy) error {
	regclass, _ := relation.regclassText()
	after, _ := policy.After.pgValue()
	schedule, _ := policy.ScheduleInterval.pgValue()
	var initial, timezone any
	if policy.InitialStart != nil {
		initial = policy.InitialStart.UTC()
	}
	if policy.Timezone != "" {
		timezone = policy.Timezone
	}
	if _, err := exec(ctx, `
CALL add_columnstore_policy(
  $1::regclass,
  after => $2::interval,
  if_not_exists => $3::boolean,
  schedule_interval => $4::interval,
  initial_start => $5::timestamptz,
  timezone => $6::text,
  created_before => NULL
)`, regclass, after, policy.IfNotExists, schedule, initial, timezone); err != nil {
		return fmt.Errorf("timescaledb: add columnstore policy for %s: %w", relationContext(relation), err)
	}
	return nil
}

func InspectColumnstorePolicy(ctx context.Context, db *pgxext.DataSource, relation Relation) (*ColumnstorePolicyMetadata, error) {
	if err := relation.validate(); err != nil {
		return nil, fmt.Errorf("timescaledb: inspect columnstore policy for %s: %w", relationContext(relation), err)
	}
	if ctx == nil || db == nil {
		return nil, fmt.Errorf("timescaledb: inspect columnstore policy for %s: nil context or DataSource", relationContext(relation))
	}
	if _, err := requireCapability(ctx, db, "columnstore policy"); err != nil {
		return nil, fmt.Errorf("timescaledb: inspect columnstore policy for %s: %w", relationContext(relation), err)
	}
	regclass, _ := relation.regclassText()
	rows, err := db.Query(ctx, `
SELECT j.job_id, j.schedule_interval, j.initial_start, internal_job.timezone,
       NULLIF(j.config->>'compress_after', '')::interval
FROM timescaledb_information.jobs j
LEFT JOIN _timescaledb_config.bgw_job internal_job ON internal_job.id = j.job_id
WHERE j.proc_name = 'policy_compression'
  AND format('%I.%I', j.hypertable_schema, j.hypertable_name)::regclass = $1::regclass
ORDER BY j.job_id
LIMIT 1`, regclass)
	if err != nil {
		return nil, fmt.Errorf("timescaledb: inspect columnstore policy for %s: %w", relationContext(relation), err)
	}
	defer closeRows(rows)
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("timescaledb: inspect columnstore policy for %s: %w", relationContext(relation), err)
		}
		return nil, fmt.Errorf("timescaledb: inspect columnstore policy for %s: %w", relationContext(relation), ErrPolicyNotFound)
	}
	var jobID int64
	var schedule, after pgtype.Interval
	var initial pgtype.Timestamptz
	var timezone pgtype.Text
	if err := rows.Scan(&jobID, &schedule, &initial, &timezone, &after); err != nil {
		return nil, fmt.Errorf("timescaledb: inspect columnstore policy for %s: %w", relationContext(relation), err)
	}
	policy := ColumnstorePolicy{InitialStart: timestamptzPointer(initial)}
	if timezone.Valid {
		policy.Timezone = timezone.String
	}
	if policy.ScheduleInterval, err = intervalFromPG(schedule); err != nil {
		return nil, fmt.Errorf("timescaledb: inspect columnstore policy for %s: %w", relationContext(relation), err)
	}
	if policy.After, err = intervalFromPG(after); err != nil {
		return nil, fmt.Errorf("timescaledb: inspect columnstore policy for %s: %w", relationContext(relation), err)
	}
	job, err := GetJob(ctx, db, jobID)
	if err != nil {
		return nil, err
	}
	if job == nil {
		return nil, fmt.Errorf("timescaledb: inspect columnstore policy for %s: job %d disappeared", relationContext(relation), jobID)
	}
	stats, err := GetJobStats(ctx, db, jobID)
	if err != nil {
		return nil, err
	}
	return &ColumnstorePolicyMetadata{Policy: policy, Job: *job, Stats: stats}, nil
}

func EnsureColumnstorePolicy(ctx context.Context, db *pgxext.DataSource, relation Relation, desired ColumnstorePolicy) (PolicyAction, error) {
	return ensureColumnstorePolicy(ctx, db, relation, desired, false)
}

func ReconcileColumnstorePolicy(ctx context.Context, db *pgxext.DataSource, relation Relation, desired ColumnstorePolicy) (PolicyAction, error) {
	return ensureColumnstorePolicy(ctx, db, relation, desired, true)
}

func ensureColumnstorePolicy(ctx context.Context, db *pgxext.DataSource, relation Relation, desired ColumnstorePolicy, reconcile bool) (PolicyAction, error) {
	desired = normalizeColumnstorePolicy(desired)
	if err := desired.Validate(); err != nil {
		return "", fmt.Errorf("timescaledb: reconcile columnstore policy for %s: %w", relationContext(relation), err)
	}
	if err := relation.validate(); err != nil {
		return "", fmt.Errorf("timescaledb: reconcile columnstore policy for %s: %w", relationContext(relation), err)
	}
	if ctx == nil || db == nil {
		return "", fmt.Errorf("timescaledb: reconcile columnstore policy for %s: nil context or DataSource", relationContext(relation))
	}
	metadata, err := InspectColumnstorePolicy(ctx, db, relation)
	var current *ColumnstorePolicy
	if err == nil {
		current = &metadata.Policy
	} else if !errors.Is(err, ErrPolicyNotFound) {
		return "", err
	}
	decision, err := DecideColumnstorePolicy(current, desired, reconcile)
	if err != nil {
		return "", err
	}
	switch decision {
	case PolicyDecisionCreate:
		if err := addColumnstorePolicy(ctx, db.Exec, relation, desired); err != nil {
			return "", err
		}
		return PolicyCreated, nil
	case PolicyDecisionNoop:
		return PolicyUnchanged, nil
	case PolicyDecisionConflict:
		return PolicyUnchanged, fmt.Errorf("timescaledb: ensure columnstore policy for %s: %w", relationContext(relation), ErrPolicyDrift)
	case PolicyDecisionReplace:
		return ReplaceColumnstorePolicy(ctx, db, relation, desired)
	default:
		return "", fmt.Errorf("timescaledb: unsupported columnstore policy decision %q", decision)
	}
}

func ReplaceColumnstorePolicy(ctx context.Context, db *pgxext.DataSource, relation Relation, desired ColumnstorePolicy) (PolicyAction, error) {
	desired = normalizeColumnstorePolicy(desired)
	if err := desired.Validate(); err != nil {
		return "", fmt.Errorf("timescaledb: replace columnstore policy for %s: %w", relationContext(relation), err)
	}
	if err := relation.validate(); err != nil {
		return "", fmt.Errorf("timescaledb: replace columnstore policy for %s: %w", relationContext(relation), err)
	}
	if ctx == nil || db == nil {
		return "", fmt.Errorf("timescaledb: replace columnstore policy for %s: nil context or DataSource", relationContext(relation))
	}
	if _, err := requireCapability(ctx, db, "columnstore policy"); err != nil {
		return "", fmt.Errorf("timescaledb: replace columnstore policy for %s: %w", relationContext(relation), err)
	}
	err := withTransaction(ctx, db, "replace columnstore policy", func(tx pgx.Tx) error {
		if err := removeColumnstorePolicy(ctx, tx.Exec, relation, true); err != nil {
			return err
		}
		return addColumnstorePolicy(ctx, tx.Exec, relation, desired)
	})
	if err != nil {
		return "", fmt.Errorf("timescaledb: replace columnstore policy for %s: %w", relationContext(relation), err)
	}
	return PolicyReplaced, nil
}

func RemoveColumnstorePolicy(ctx context.Context, db *pgxext.DataSource, relation Relation, ifExists bool) error {
	if err := relation.validate(); err != nil {
		return fmt.Errorf("timescaledb: remove columnstore policy for %s: %w", relationContext(relation), err)
	}
	if ctx == nil || db == nil {
		return fmt.Errorf("timescaledb: remove columnstore policy for %s: nil context or DataSource", relationContext(relation))
	}
	if _, err := requireCapability(ctx, db, "columnstore policy"); err != nil {
		return fmt.Errorf("timescaledb: remove columnstore policy for %s: %w", relationContext(relation), err)
	}
	return removeColumnstorePolicy(ctx, db.Exec, relation, ifExists)
}

func removeColumnstorePolicy(ctx context.Context, exec func(context.Context, string, ...any) (pgconn.CommandTag, error), relation Relation, ifExists bool) error {
	regclass, _ := relation.regclassText()
	if _, err := exec(ctx, `CALL remove_columnstore_policy($1::regclass, if_exists => $2::boolean)`, regclass, ifExists); err != nil {
		return fmt.Errorf("timescaledb: remove columnstore policy for %s: %w", relationContext(relation), err)
	}
	return nil
}
