package timescaledb

import (
	"strings"
	"testing"
	"time"
)

func TestContinuousAggregateSafeDefaults(t *testing.T) {
	aggregate := NewContinuousAggregate(
		Relation{Schema: "metrics", Name: "observations_15s"},
		Relation{Schema: "metrics", Name: "observations"},
		"observed_at",
	).
		Bucket(FixedInterval(15 * time.Second)).
		Dimension("series_id").
		Measure(Avg("value").As("average"))
	sql, err := aggregate.BuildSQL()
	if err != nil {
		t.Fatalf("BuildSQL: %v", err)
	}
	for _, want := range []string{
		`CREATE MATERIALIZED VIEW "metrics"."observations_15s"`,
		`timescaledb.continuous`,
		`timescaledb.create_group_indexes = TRUE`,
		`timescaledb.materialized_only = TRUE`,
		`time_bucket(INTERVAL '15000000 microseconds', "observed_at") AS "bucket"`,
		`AVG("value") AS "average"`,
		`GROUP BY 1, 2`,
		`WITH NO DATA;`,
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("SQL missing %q:\n%s", want, sql)
		}
	}
}

func TestContinuousAggregateRealTimeInversion(t *testing.T) {
	aggregate := NewContinuousAggregate(Relation{Name: "frames"}, Relation{Name: "events"}, "time").
		Bucket(FixedInterval(time.Minute)).Measure(Count().As("count")).RealTime(true)
	sql, err := aggregate.BuildSQL()
	if err != nil {
		t.Fatalf("BuildSQL: %v", err)
	}
	if !strings.Contains(sql, `timescaledb.materialized_only = FALSE`) {
		t.Fatalf("real-time SQL is not inverted:\n%s", sql)
	}
	runtimeSQL, err := buildSetRealTimeSQL(Relation{Schema: "metrics", Name: "frames"}, true)
	if err != nil {
		t.Fatalf("buildSetRealTimeSQL: %v", err)
	}
	if !strings.Contains(runtimeSQL, `timescaledb.materialized_only = FALSE`) {
		t.Fatalf("runtime SQL = %s", runtimeSQL)
	}
	runtimeSQL, err = buildSetRealTimeSQL(Relation{Schema: "metrics", Name: "frames"}, false)
	if err != nil {
		t.Fatalf("buildSetRealTimeSQL disabled: %v", err)
	}
	if !strings.Contains(runtimeSQL, `timescaledb.materialized_only = TRUE`) {
		t.Fatalf("runtime SQL = %s", runtimeSQL)
	}
}

func TestContinuousAggregateRejectsGapfill(t *testing.T) {
	_, err := NewContinuousAggregate(Relation{Name: "frames"}, Relation{Name: "events"}, "time").
		Bucket(FixedInterval(time.Minute)).
		Measure(Avg("value").As("average").LOCF(true)).
		BuildSQL()
	if err == nil || !strings.Contains(err.Error(), "gap filling") {
		t.Fatalf("filled measure error = %v", err)
	}
	_, err = NewContinuousAggregate(Relation{Name: "frames"}, Relation{Name: "events"}, "time").
		Select(TrustedExpression(`SELECT time_bucket_gapfill('1 hour', time), count(*) FROM events GROUP BY 1`)).
		BuildSQL()
	if err == nil || !strings.Contains(err.Error(), "time_bucket_gapfill") {
		t.Fatalf("trusted gapfill error = %v", err)
	}
}

func TestContinuousAggregateTrustedSelectAcceptsTrailingSemicolon(t *testing.T) {
	sql, err := NewContinuousAggregate(Relation{Name: "frames"}, Relation{Name: "events"}, "time").
		Select(TrustedExpression(`SELECT time_bucket(INTERVAL '1 minute', time) AS bucket, count(*) AS count FROM events GROUP BY 1;`)).
		BuildSQL()
	if err != nil {
		t.Fatalf("BuildSQL: %v", err)
	}
	if strings.Contains(sql, "GROUP BY 1;\nWITH NO DATA") {
		t.Fatalf("trusted SELECT retained a statement terminator:\n%s", sql)
	}
	if !strings.Contains(sql, "GROUP BY 1\nWITH NO DATA;") {
		t.Fatalf("unexpected trusted SELECT SQL:\n%s", sql)
	}
}
