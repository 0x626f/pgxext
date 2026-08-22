package timescaledb

import (
	"strings"
	"testing"
	"time"
)

func TestExistingHypertableBuildSQL(t *testing.T) {
	sql, err := NewHypertable(Relation{Schema: "metrics", Name: "observations"}, "observed_at").
		ChunkInterval(FixedInterval(24*time.Hour)).
		IfNotExists().
		CreateDefaultIndexes(false).
		MigrateExistingData().
		HashDimension("series_id", 4).
		BuildSQL()
	if err != nil {
		t.Fatalf("BuildSQL: %v", err)
	}
	for _, want := range []string{
		`create_hypertable('"metrics"."observations"'::regclass, by_range('observed_at', INTERVAL '86400000000 microseconds')`,
		`create_default_indexes => FALSE`,
		`if_not_exists => TRUE`,
		`migrate_data => TRUE`,
		`add_dimension('"metrics"."observations"'::regclass, by_hash('series_id', 4), if_not_exists => TRUE)`,
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("SQL missing %q:\n%s", want, sql)
		}
	}
}

func TestModernHypertableBuildSQL(t *testing.T) {
	settings := NewColumnstoreSettings().
		SegmentBy("series_id").
		OrderBy(ColumnstoreDescending("observed_at").NullsLast())
	sql, err := NewHypertableTable(
		Relation{Schema: "metrics", Name: "observations"},
		"observed_at",
		TrustedExpression(`  "observed_at" TIMESTAMPTZ NOT NULL,
  "series_id" TEXT NOT NULL,
  "value" DOUBLE PRECISION  `),
	).
		IfNotExists().
		ChunkInterval(FixedInterval(15*time.Second)).
		Columnstore(settings).
		HashDimension("series_id", 2).
		BuildSQL()
	if err != nil {
		t.Fatalf("BuildSQL: %v", err)
	}
	for _, want := range []string{
		`CREATE TABLE IF NOT EXISTS "metrics"."observations"`,
		`tsdb.hypertable`,
		`tsdb.partition_column = 'observed_at'`,
		`tsdb.chunk_interval = '15000000 microseconds'`,
		`tsdb.segmentby = '"series_id"'`,
		`tsdb.orderby = '"observed_at" DESC NULLS LAST'`,
		`SELECT add_dimension`,
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("SQL missing %q:\n%s", want, sql)
		}
	}
}

func TestHypertableValidation(t *testing.T) {
	if _, err := NewHypertable(Relation{Name: "events"}, "time").HashDimension("key", 0).BuildSQL(); err == nil {
		t.Fatal("expected invalid hash partition count")
	}
	if _, err := NewHypertableTable(Relation{Name: "events"}, "time", "").BuildSQL(); err == nil {
		t.Fatal("expected empty trusted definition error")
	}
}

func TestSetChunkIntervalSQL(t *testing.T) {
	sql, err := SetChunkIntervalSQL(Relation{Schema: "metrics", Name: "observations"}, FixedInterval(time.Hour))
	if err != nil {
		t.Fatalf("SetChunkIntervalSQL: %v", err)
	}
	want := `SELECT set_chunk_time_interval('"metrics"."observations"'::regclass, INTERVAL '3600000000 microseconds');`
	if sql != want {
		t.Fatalf("SQL = %q, want %q", sql, want)
	}
}
