package timescaledb

import "fmt"

// LifecycleConfig describes application-owned data lifecycle boundaries. An
// open-ended interval means that boundary is not declared and its related
// diagnostic is skipped.
type LifecycleConfig struct {
	LateArrivalWindow  Interval
	ColumnstoreAfter   Interval
	RawRetentionAfter  Interval
	RefreshStartOffset Interval
	BackfillWindow     Interval
}

type DiagnosticSeverity string

const (
	DiagnosticWarning DiagnosticSeverity = "warning"
	DiagnosticError   DiagnosticSeverity = "error"
)

type LifecycleDiagnosticCode string

const (
	ColumnstoreBeforeLateArrival LifecycleDiagnosticCode = "columnstore_before_late_arrival"
	ColumnstoreAfterRetention    LifecycleDiagnosticCode = "columnstore_after_retention"
	RefreshBeyondRetention       LifecycleDiagnosticCode = "refresh_beyond_retention"
	BackfillBeyondRetention      LifecycleDiagnosticCode = "backfill_beyond_retention"
)

// LifecycleDiagnostic reports an unsafe combination without rewriting it.
type LifecycleDiagnostic struct {
	Code     LifecycleDiagnosticCode
	Severity DiagnosticSeverity
	Message  string
}

// ValidateLifecycle returns structured diagnostics. It accepts fixed intervals
// and comparable whole-month intervals, but rejects comparisons between the two
// because their ordering depends on calendar boundaries.
func ValidateLifecycle(config LifecycleConfig) ([]LifecycleDiagnostic, error) {
	values := []struct {
		name     string
		interval Interval
	}{
		{"late-arrival window", config.LateArrivalWindow},
		{"columnstore age", config.ColumnstoreAfter},
		{"raw retention age", config.RawRetentionAfter},
		{"refresh start offset", config.RefreshStartOffset},
		{"backfill window", config.BackfillWindow},
	}
	for _, value := range values {
		if value.interval.IsOpenEnded() {
			continue
		}
		if err := value.interval.Validate(); err != nil {
			return nil, fmt.Errorf("timescaledb: invalid lifecycle %s: %w", value.name, err)
		}
	}
	diagnostics := make([]LifecycleDiagnostic, 0)
	compare := func(leftName string, left Interval, rightName string, right Interval) (int, bool, error) {
		if left.IsOpenEnded() || right.IsOpenEnded() {
			return 0, false, nil
		}
		result, err := compareIntervals(left, right)
		if err != nil {
			return 0, false, fmt.Errorf("timescaledb: compare lifecycle %s and %s: %w", leftName, rightName, err)
		}
		return result, true, nil
	}
	if comparison, present, err := compare("columnstore age", config.ColumnstoreAfter, "late-arrival window", config.LateArrivalWindow); err != nil {
		return nil, err
	} else if present && comparison <= 0 {
		diagnostics = append(diagnostics, LifecycleDiagnostic{
			Code: ColumnstoreBeforeLateArrival, Severity: DiagnosticWarning,
			Message: "columnstore conversion is scheduled before the declared late-update window closes",
		})
	}
	if comparison, present, err := compare("columnstore age", config.ColumnstoreAfter, "raw retention age", config.RawRetentionAfter); err != nil {
		return nil, err
	} else if present && comparison >= 0 {
		diagnostics = append(diagnostics, LifecycleDiagnostic{
			Code: ColumnstoreAfterRetention, Severity: DiagnosticError,
			Message: "columnstore age must be less than raw-data retention age or chunks can be deleted before conversion",
		})
	}
	if comparison, present, err := compare("refresh start offset", config.RefreshStartOffset, "raw retention age", config.RawRetentionAfter); err != nil {
		return nil, err
	} else if present && comparison > 0 {
		diagnostics = append(diagnostics, LifecycleDiagnostic{
			Code: RefreshBeyondRetention, Severity: DiagnosticError,
			Message: "continuous-aggregate refresh window reaches raw history already eligible for deletion",
		})
	}
	if comparison, present, err := compare("backfill window", config.BackfillWindow, "raw retention age", config.RawRetentionAfter); err != nil {
		return nil, err
	} else if present && comparison > 0 {
		diagnostics = append(diagnostics, LifecycleDiagnostic{
			Code: BackfillBeyondRetention, Severity: DiagnosticError,
			Message: "declared backfill window requires raw data older than the retention boundary",
		})
	}
	return diagnostics, nil
}
