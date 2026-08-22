package timescaledb

import (
	"strings"
	"testing"
	"time"
)

func TestPlanRefreshPolicyFormula(t *testing.T) {
	policy, err := PlanRefreshPolicy(15*time.Second, 10*time.Minute, 0)
	if err != nil {
		t.Fatalf("PlanRefreshPolicy: %v", err)
	}
	start, _ := policy.StartOffset.Duration()
	end, _ := policy.EndOffset.Duration()
	schedule, _ := policy.ScheduleInterval.Duration()
	if start != 10*time.Minute+15*time.Second || end != 15*time.Second || schedule != 15*time.Second {
		t.Fatalf("policy = %+v", policy)
	}
	if !policy.RefreshNewestFirst {
		t.Fatal("planner should refresh newest first")
	}
	policy, err = PlanRefreshPolicy(time.Minute, 5*time.Second, 30*time.Second)
	if err != nil {
		t.Fatalf("PlanRefreshPolicy: %v", err)
	}
	start, _ = policy.StartOffset.Duration()
	if start != 2*time.Minute {
		t.Fatalf("start = %v, want 2m", start)
	}
}

func TestRefreshPolicyValidation(t *testing.T) {
	valid := RefreshPolicy{
		StartOffset: FixedInterval(time.Hour), EndOffset: FixedInterval(time.Minute),
		ScheduleInterval: FixedInterval(time.Minute), RefreshNewestFirst: true,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid policy: %v", err)
	}
	invalid := valid
	invalid.StartOffset = FixedInterval(time.Minute)
	invalid.EndOffset = FixedInterval(time.Hour)
	if err := invalid.Validate(); err == nil || !strings.Contains(err.Error(), "greater") {
		t.Fatalf("offset error = %v", err)
	}
	invalid = valid
	invalid.EndOffset = OpenEndedInterval()
	if err := invalid.Validate(); err == nil || !strings.Contains(err.Error(), "AllowIncompleteBucket") {
		t.Fatalf("open end error = %v", err)
	}
	invalid.AllowIncompleteBucket = true
	if err := invalid.Validate(); err != nil {
		t.Fatalf("explicit open end: %v", err)
	}
	invalid = valid
	invalid.Timezone = "Not/AZone"
	if err := invalid.Validate(); err == nil {
		t.Fatal("expected timezone error")
	}
	invalid.Timezone = "Local"
	if err := invalid.Validate(); err == nil {
		t.Fatal("expected Go Local pseudo-timezone error")
	}
}

func TestRefreshPolicyComparisonAndDecision(t *testing.T) {
	policy, _ := PlanRefreshPolicy(time.Minute, 5*time.Minute, time.Minute)
	copy := policy
	copy.IfNotExists = true
	if !RefreshPoliciesEqual(policy, copy) {
		t.Fatal("IfNotExists should not affect semantic equality")
	}
	decision, err := DecideRefreshPolicy(nil, policy, true)
	if err != nil || decision != PolicyDecisionCreate {
		t.Fatalf("create decision = %q, %v", decision, err)
	}
	decision, err = DecideRefreshPolicy(&copy, policy, true)
	if err != nil || decision != PolicyDecisionNoop {
		t.Fatalf("noop decision = %q, %v", decision, err)
	}
	drift := copy
	drift.ScheduleInterval = FixedInterval(2 * time.Minute)
	decision, _ = DecideRefreshPolicy(&drift, policy, false)
	if decision != PolicyDecisionConflict {
		t.Fatalf("conflict decision = %q", decision)
	}
	decision, _ = DecideRefreshPolicy(&drift, policy, true)
	if decision != PolicyDecisionReplace {
		t.Fatalf("replace decision = %q", decision)
	}
	includeTiered := true
	drift.IncludeTieredData = &includeTiered
	if RefreshPoliciesEqual(drift, policy) {
		t.Fatal("include_tiered_data must participate in semantic equality")
	}
}

func TestRetentionAndColumnstoreSemanticDefaults(t *testing.T) {
	retention := NewRetentionPolicy(FixedInterval(30 * 24 * time.Hour))
	zeroSchedule := RetentionPolicy{DropAfter: retention.DropAfter}
	if !RetentionPoliciesEqual(retention, zeroSchedule) {
		t.Fatal("retention default schedule should be semantic")
	}
	columnstore := NewColumnstorePolicy(FixedInterval(7 * 24 * time.Hour))
	zeroColumnstore := ColumnstorePolicy{After: columnstore.After}
	if !ColumnstorePoliciesEqual(columnstore, zeroColumnstore) {
		t.Fatal("columnstore default schedule should be semantic")
	}
	if decision, err := DecideRetentionPolicy(&retention, zeroSchedule, true); err != nil || decision != PolicyDecisionNoop {
		t.Fatalf("retention noop decision = %q, %v", decision, err)
	}
	retentionDrift := retention
	retentionDrift.DropAfter = FixedInterval(31 * 24 * time.Hour)
	if decision, err := DecideRetentionPolicy(&retentionDrift, retention, true); err != nil || decision != PolicyDecisionReplace {
		t.Fatalf("retention replace decision = %q, %v", decision, err)
	}
	if decision, err := DecideColumnstorePolicy(&columnstore, zeroColumnstore, true); err != nil || decision != PolicyDecisionNoop {
		t.Fatalf("columnstore noop decision = %q, %v", decision, err)
	}
	columnstoreDrift := columnstore
	columnstoreDrift.After = FixedInterval(8 * 24 * time.Hour)
	if decision, err := DecideColumnstorePolicy(&columnstoreDrift, columnstore, true); err != nil || decision != PolicyDecisionReplace {
		t.Fatalf("columnstore replace decision = %q, %v", decision, err)
	}
}

func TestPlanRefreshBatchesBounded(t *testing.T) {
	start := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	ranges, err := PlanRefreshBatches(start, start.Add(50*time.Minute), 20*time.Minute, 3)
	if err != nil {
		t.Fatalf("PlanRefreshBatches: %v", err)
	}
	if len(ranges) != 3 || !ranges[2].End.Equal(start.Add(50*time.Minute)) {
		t.Fatalf("ranges = %+v", ranges)
	}
	if _, err := PlanRefreshBatches(start, start.Add(time.Hour), 10*time.Minute, 5); err == nil {
		t.Fatal("expected maxBatches error")
	}
}
