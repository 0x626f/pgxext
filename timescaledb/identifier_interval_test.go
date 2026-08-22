package timescaledb

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestRelationQuotesHostileLookingIdentifiers(t *testing.T) {
	relation := Relation{Schema: "metrics.schema", Name: `observations"; DROP TABLE x;--`}
	got, err := relation.quoted()
	if err != nil {
		t.Fatalf("quoted: %v", err)
	}
	want := `"metrics.schema"."observations""; DROP TABLE x;--"`
	if got != want {
		t.Fatalf("quoted = %s, want %s", got, want)
	}
}

func TestIdentifiersRejectEmptyAndNUL(t *testing.T) {
	for _, test := range []struct {
		name string
		fn   func() error
	}{
		{"empty relation", func() error { return (Relation{}).validate() }},
		{"relation NUL", func() error { return (Relation{Name: "bad\x00name"}).validate() }},
		{"empty column", func() error { return Column("").validate() }},
		{"alias NUL", func() error { return Alias("bad\x00alias").validate() }},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := test.fn(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestSQLLiteralEscapesQuotesAndBackslashes(t *testing.T) {
	got := sqlLiteral(`name\'; DROP TABLE observations;--`)
	want := `E'name\\''; DROP TABLE observations;--'`
	if got != want {
		t.Fatalf("sqlLiteral = %q, want %q", got, want)
	}
}

func TestIntervalRendering(t *testing.T) {
	fixed := FixedInterval(15 * time.Second)
	if got, err := fixed.SQL(); err != nil || got != "INTERVAL '15000000 microseconds'" {
		t.Fatalf("fixed SQL = %q, %v", got, err)
	}
	calendar := CalendarMonths(3)
	if got, err := calendar.SQL(); err != nil || got != "INTERVAL '3 months'" {
		t.Fatalf("calendar SQL = %q, %v", got, err)
	}
	value, err := fixed.pgValue()
	if err != nil {
		t.Fatalf("pgValue: %v", err)
	}
	interval := value.(pgtype.Interval)
	if interval.Microseconds != 15_000_000 || interval.Months != 0 || !interval.Valid {
		t.Fatalf("unexpected pg interval: %+v", interval)
	}
}

func TestIntervalRejectsUnsupportedWidths(t *testing.T) {
	for _, interval := range []Interval{
		OpenEndedInterval(),
		FixedInterval(0),
		FixedInterval(-time.Second),
		FixedInterval(time.Nanosecond),
		CalendarMonths(0),
		CalendarMonths(-1),
	} {
		if err := interval.Validate(); err == nil {
			t.Fatalf("Validate(%+v) unexpectedly succeeded", interval)
		}
	}
	_, err := intervalFromPG(pgtype.Interval{Months: 1, Days: 1, Valid: true})
	if err == nil || !strings.Contains(err.Error(), "mixed") {
		t.Fatalf("mixed interval error = %v", err)
	}
	for _, value := range []pgtype.Interval{
		{Days: 1, Microseconds: math.MaxInt64, Valid: true},
		{Days: -1, Microseconds: math.MinInt64, Valid: true},
	} {
		if _, err := intervalFromPG(value); err == nil || !strings.Contains(err.Error(), "overflows") {
			t.Fatalf("overflow interval error = %v", err)
		}
	}
}
