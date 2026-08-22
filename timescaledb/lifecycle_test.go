package timescaledb

import (
	"testing"
	"time"
)

func TestValidateLifecycleDiagnostics(t *testing.T) {
	diagnostics, err := ValidateLifecycle(LifecycleConfig{
		LateArrivalWindow:  FixedInterval(2 * time.Hour),
		ColumnstoreAfter:   FixedInterval(time.Hour),
		RawRetentionAfter:  FixedInterval(7 * 24 * time.Hour),
		RefreshStartOffset: FixedInterval(8 * 24 * time.Hour),
		BackfillWindow:     FixedInterval(10 * 24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("ValidateLifecycle: %v", err)
	}
	want := map[LifecycleDiagnosticCode]bool{
		ColumnstoreBeforeLateArrival: false,
		RefreshBeyondRetention:       false,
		BackfillBeyondRetention:      false,
	}
	for _, diagnostic := range diagnostics {
		if _, exists := want[diagnostic.Code]; exists {
			want[diagnostic.Code] = true
		}
	}
	for code, found := range want {
		if !found {
			t.Errorf("missing diagnostic %s: %+v", code, diagnostics)
		}
	}
}

func TestValidateLifecycleSafeConfiguration(t *testing.T) {
	diagnostics, err := ValidateLifecycle(LifecycleConfig{
		LateArrivalWindow:  FixedInterval(time.Hour),
		ColumnstoreAfter:   FixedInterval(2 * time.Hour),
		RawRetentionAfter:  FixedInterval(30 * 24 * time.Hour),
		RefreshStartOffset: FixedInterval(24 * time.Hour),
		BackfillWindow:     FixedInterval(7 * 24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("ValidateLifecycle: %v", err)
	}
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %+v", diagnostics)
	}
}

func TestValidateLifecycleRequiresColumnstoreAfterLateArrival(t *testing.T) {
	diagnostics, err := ValidateLifecycle(LifecycleConfig{
		LateArrivalWindow: FixedInterval(time.Hour),
		ColumnstoreAfter:  FixedInterval(time.Hour),
	})
	if err != nil {
		t.Fatalf("ValidateLifecycle: %v", err)
	}
	if len(diagnostics) != 1 || diagnostics[0].Code != ColumnstoreBeforeLateArrival {
		t.Fatalf("diagnostics = %+v", diagnostics)
	}
}
