package timescaledb

import (
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

type testFrame struct {
	Bucket   time.Time `db:"bucket"`
	SeriesID string    `db:"series_id"`
	Average  *float64  `db:"average"`
}

func TestFrameBuildSQLAndArgumentOrder(t *testing.T) {
	from := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	to := from.Add(time.Minute)
	query := NewFrameQuery[testFrame](Relation{Schema: "metrics", Name: "observations"}, "observed_at").
		Bucket(FixedInterval(15*time.Second)).
		Dimension("series_id").
		Measure(Avg("value").As("average")).
		Between(from, to).
		Where(Equal("series_id", "alpha"), GreaterThan("value", 10.0)).
		OrderAscending()
	sql, args, err := query.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	wantSQL := `SELECT time_bucket($1::interval, "observed_at") AS "bucket", "series_id", AVG("value") AS "average"
FROM "metrics"."observations"
WHERE "observed_at" >= $2::timestamptz
  AND "observed_at" < $3::timestamptz
  AND "series_id" = $4
  AND "value" > $5
GROUP BY 1, 2
ORDER BY 1 ASC, 2 ASC`
	if sql != wantSQL {
		t.Fatalf("SQL mismatch:\n%s\nwant:\n%s", sql, wantSQL)
	}
	if len(args) != 5 {
		t.Fatalf("len(args) = %d", len(args))
	}
	width, ok := args[0].(pgtype.Interval)
	if !ok || width.Microseconds != 15_000_000 {
		t.Fatalf("width arg = %#v", args[0])
	}
	if args[1] != from || args[2] != to || args[3] != "alpha" || args[4] != 10.0 {
		t.Fatalf("args = %#v", args)
	}
}

func TestFrameAlignmentOptions(t *testing.T) {
	from := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	origin := from.Add(-time.Hour)
	query := NewFrameQuery[testFrame](Relation{Name: "observations"}, "observed_at").
		Bucket(FixedInterval(time.Hour)).
		Timezone("Europe/Madrid").
		Origin(origin).
		Offset(FixedInterval(-5*time.Minute)).
		Measure(Count().As("average")).
		Between(from, from.Add(2*time.Hour))
	sql, args, err := query.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, want := range []string{
		`timezone => $2::text`,
		`origin => $3::timestamptz`,
		`"offset" => $4::interval`,
		`"observed_at" >= $5::timestamptz`,
		`"observed_at" < $6::timestamptz`,
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("SQL missing %q:\n%s", want, sql)
		}
	}
	if len(args) != 6 || args[1] != "Europe/Madrid" || args[2] != origin {
		t.Fatalf("args = %#v", args)
	}
}

func TestFrameRejectsOriginAndOffsetWithoutTimezone(t *testing.T) {
	from := time.Now().UTC()
	_, _, err := NewFrameQuery[testFrame](Relation{Name: "events"}, "time").
		Bucket(FixedInterval(time.Hour)).
		Origin(from).
		Offset(FixedInterval(time.Minute)).
		Measure(Count().As("count")).
		Between(from, from.Add(time.Hour)).
		Build()
	if err == nil || !strings.Contains(err.Error(), "require the timezone overload") {
		t.Fatalf("error = %v", err)
	}
}

func TestGapfillNullLOCFAndInterpolation(t *testing.T) {
	from := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	query := NewFrameQuery[testFrame](Relation{Name: "observations"}, "observed_at").
		Bucket(FixedInterval(15*time.Second)).
		GapFill().
		Dimension("series_id").
		Measure(
			Avg("value").As("nullable_average"),
			Avg("value").As("carried").LOCF(true).PreviousLookup(TrustedExpression(`SELECT 1.0`)),
			Avg("value").As("interpolated").Interpolate().
				PreviousLookup(TrustedExpression(`SELECT (TIMESTAMPTZ '2024-12-31', 1.0)`)).
				NextLookup(TrustedExpression(`SELECT (TIMESTAMPTZ '2025-01-02', 2.0)`)),
		).
		Between(from, from.Add(time.Minute))
	sql, _, err := query.Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, want := range []string{
		`time_bucket_gapfill(`,
		`AVG("value") AS "nullable_average"`,
		`locf(AVG("value"), prev => (SELECT 1.0), treat_null_as_missing => TRUE) AS "carried"`,
		`interpolate(AVG("value"), prev => (SELECT (TIMESTAMPTZ '2024-12-31', 1.0)), next => (SELECT (TIMESTAMPTZ '2025-01-02', 2.0))) AS "interpolated"`,
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("SQL missing %q:\n%s", want, sql)
		}
	}
}

func TestGapfillRequiresBoundedRangeAndRejectsAlignment(t *testing.T) {
	_, _, err := NewFrameQuery[testFrame](Relation{Name: "events"}, "time").
		Bucket(FixedInterval(time.Minute)).GapFill().Measure(Count().As("count")).Build()
	if err == nil || !strings.Contains(err.Error(), "bounded") {
		t.Fatalf("unbounded error = %v", err)
	}
	from := time.Now().UTC()
	_, _, err = NewFrameQuery[testFrame](Relation{Name: "events"}, "time").
		Bucket(FixedInterval(time.Minute)).GapFill().Origin(from).
		Measure(Count().As("count")).Between(from, from.Add(time.Hour)).Build()
	if err == nil || !strings.Contains(err.Error(), "not origin or offset") {
		t.Fatalf("alignment error = %v", err)
	}
}

func TestFrameRejectsDuplicateOutputs(t *testing.T) {
	from := time.Now().UTC()
	_, _, err := NewFrameQuery[testFrame](Relation{Name: "events"}, "time").
		Bucket(FixedInterval(time.Minute)).
		Dimension("series").Dimension("series").
		Measure(Count().As("count")).
		Between(from, from.Add(time.Hour)).Build()
	if err == nil || !strings.Contains(err.Error(), "duplicate dimension") {
		t.Fatalf("error = %v", err)
	}
}

func TestFrameQuotesHostileAliasAndRejectsIgnoredLookup(t *testing.T) {
	from := time.Now().UTC()
	sql, _, err := NewFrameQuery[testFrame](Relation{Name: "events"}, "time").
		Bucket(FixedInterval(time.Minute)).
		Measure(Count().As(Alias(`select"; DROP TABLE events;--`))).
		Between(from, from.Add(time.Hour)).Build()
	if err != nil {
		t.Fatalf("Build hostile alias: %v", err)
	}
	if !strings.Contains(sql, `AS "select""; DROP TABLE events;--"`) {
		t.Fatalf("alias was not safely quoted:\n%s", sql)
	}

	_, _, err = NewFrameQuery[testFrame](Relation{Name: "events"}, "time").
		Bucket(FixedInterval(time.Minute)).
		Measure(Avg("value").As("average").PreviousLookup(TrustedExpression(`SELECT 1`))).
		Between(from, from.Add(time.Hour)).Build()
	if err == nil || !strings.Contains(err.Error(), "requires LOCF or interpolation") {
		t.Fatalf("ignored lookup error = %v", err)
	}
}
