package timescaledb_test

import (
	"context"
	"errors"
	"math"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/0x626f/pgxext"
	"github.com/0x626f/pgxext/migration"
	"github.com/0x626f/pgxext/timescaledb"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

const integrationSchema = "pgxext_timescaledb_test"

type integrationFrame struct {
	Bucket   time.Time `db:"bucket"`
	SeriesID string    `db:"series_id"`
	Average  *float64  `db:"average"`
}

type integrationAggregateFrame struct {
	Bucket      time.Time `db:"bucket"`
	SeriesID    string    `db:"series_id"`
	SampleCount int64     `db:"sample_count"`
	Total       float64   `db:"total"`
	Average     float64   `db:"average"`
	Minimum     float64   `db:"minimum"`
	Maximum     float64   `db:"maximum"`
	First       float64   `db:"first_value"`
	Last        float64   `db:"last_value"`
}

func timescaleDataSource(t *testing.T) *pgxext.DataSource {
	t.Helper()
	url := os.Getenv("TEST_TIMESCALE_DATABASE_URL")
	if url == "" {
		if os.Getenv("PGXEXT_REQUIRE_INTEGRATION") == "1" {
			t.Fatal("TEST_TIMESCALE_DATABASE_URL not set while integration tests are required")
		}
		t.Skip("TEST_TIMESCALE_DATABASE_URL not set; skipping TimescaleDB integration test")
	}
	config := pgxext.NewConfig()
	if _, err := config.WithURL(url); err != nil {
		t.Fatalf("config: %v", err)
	}
	ds, err := pgxext.NewDataSource(context.Background(), config)
	if err != nil {
		t.Fatalf("NewDataSource: %v", err)
	}
	t.Cleanup(ds.Close)
	return ds
}

func TestTimescaleDBIntegration(t *testing.T) {
	db := timescaleDataSource(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	assertTimescalePostgreSQLMajor(t, ctx, db)

	installed, err := timescaledb.ExtensionInstalled(ctx, db)
	if err != nil {
		t.Fatalf("ExtensionInstalled: %v", err)
	}
	if !installed {
		if err := timescaledb.CreateExtension(ctx, db); err != nil {
			t.Fatalf("CreateExtension: %v", err)
		}
	}
	// CREATE EXTENSION IF NOT EXISTS is deliberately explicit and idempotent.
	if err := timescaledb.CreateExtension(ctx, db); err != nil {
		t.Fatalf("idempotent CreateExtension: %v", err)
	}
	version, err := timescaledb.InstalledVersion(ctx, db)
	if err != nil || version == "" {
		t.Fatalf("InstalledVersion = %q, %v", version, err)
	}
	expectedVersion := os.Getenv("TEST_TIMESCALE_EXTENSION_VERSION")
	if expectedVersion == "" && os.Getenv("PGXEXT_REQUIRE_INTEGRATION") == "1" {
		t.Fatal("TEST_TIMESCALE_EXTENSION_VERSION not set while integration tests are required")
	}
	if expectedVersion != "" && version != expectedVersion {
		t.Fatalf("TimescaleDB extension version = %q, want %q", version, expectedVersion)
	}
	capabilities, err := timescaledb.InspectCapabilities(ctx, db)
	if err != nil {
		t.Fatalf("InspectCapabilities: %v", err)
	}
	if !capabilities.Compatible || !capabilities.ModernHypertableCreate ||
		!capabilities.Gapfill || !capabilities.ContinuousAggregates ||
		!capabilities.RealTimeAggregates ||
		!capabilities.RefreshPolicies || !capabilities.ManualRefresh ||
		!capabilities.RetentionPolicies || !capabilities.Columnstore ||
		!capabilities.RefreshPolicyBatching || !capabilities.RefreshNewestFirst ||
		!capabilities.TIMESTAMPTZTimeDimensions || capabilities.IntegerTimeDimensions {
		t.Fatalf("capabilities = %+v", capabilities)
	}

	cleanup := func() {
		_, _ = db.Exec(context.Background(), `DROP SCHEMA IF EXISTS `+integrationSchema+` CASCADE`)
	}
	cleanup()
	t.Cleanup(cleanup)
	if _, err := db.Exec(ctx, `CREATE SCHEMA `+integrationSchema); err != nil {
		t.Fatalf("create schema: %v", err)
	}

	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	source := timescaledb.Relation{Schema: integrationSchema, Name: "observations"}
	if _, err := db.Exec(ctx, `
CREATE TABLE `+integrationSchema+`.observations (
  observed_at TIMESTAMPTZ NOT NULL,
  series_id TEXT NOT NULL,
  value DOUBLE PRECISION NOT NULL
)`); err != nil {
		t.Fatalf("create ordinary table: %v", err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO `+integrationSchema+`.observations (observed_at, series_id, value) VALUES ($1, $2, $3)`, base.Add(-time.Hour), "migrated", 1.0); err != nil {
		t.Fatalf("insert pre-conversion observation: %v", err)
	}

	conversion := timescaledb.NewHypertable(source, "observed_at").
		ChunkInterval(timescaledb.FixedInterval(15*time.Second)).
		HashDimension("series_id", 2).
		MigrateExistingData().
		IfNotExists()
	if err := conversion.Apply(ctx, db); err != nil {
		t.Fatalf("convert hypertable: %v", err)
	}
	if err := conversion.Apply(ctx, db); err != nil {
		t.Fatalf("idempotent hypertable conversion: %v", err)
	}
	if err := timescaledb.SetChunkInterval(ctx, db, source, timescaledb.FixedInterval(30*time.Second)); err != nil {
		t.Fatalf("SetChunkInterval: %v", err)
	}
	if err := timescaledb.SetChunkInterval(ctx, db, source, timescaledb.FixedInterval(15*time.Second)); err != nil {
		t.Fatalf("restore chunk interval: %v", err)
	}
	metadata, err := timescaledb.GetHypertable(ctx, db, source)
	if err != nil {
		t.Fatalf("GetHypertable: %v", err)
	}
	if metadata.PrimaryDimension != "observed_at" || metadata.PrimaryDimensionType != "timestamp with time zone" || metadata.NumDimensions != 2 {
		t.Fatalf("hypertable metadata = %+v", metadata)
	}
	hypertables, err := timescaledb.ListHypertables(ctx, db)
	if err != nil || !containsHypertable(hypertables, source) {
		t.Fatalf("ListHypertables = %+v, %v", hypertables, err)
	}
	dimensions, err := timescaledb.ListDimensions(ctx, db)
	if err != nil || !containsDimension(dimensions, source, "observed_at") || !containsDimension(dimensions, source, "series_id") {
		t.Fatalf("ListDimensions = %+v, %v", dimensions, err)
	}
	for _, row := range []struct {
		offset time.Duration
		value  float64
	}{
		{0, 10},
		{10 * time.Second, 20},
		{30 * time.Second, 30},
		{45 * time.Second, 40},
	} {
		if _, err := db.Exec(ctx, `INSERT INTO `+integrationSchema+`.observations (observed_at, series_id, value) VALUES ($1, $2, $3)`, base.Add(row.offset), "alpha", row.value); err != nil {
			t.Fatalf("insert observation: %v", err)
		}
	}
	chunks, err := timescaledb.ListChunks(ctx, db, source)
	if err != nil || len(chunks) == 0 {
		t.Fatalf("ListChunks = %+v, %v", chunks, err)
	}

	ordinary, err := timescaledb.NewFrameQuery[integrationFrame](source, "observed_at").
		Bucket(timescaledb.FixedInterval(15*time.Second)).
		Dimension("series_id").
		Measure(timescaledb.Avg("value").As("average")).
		Between(base, base.Add(time.Minute)).
		Where(timescaledb.Equal("series_id", "alpha")).
		Execute(ctx, db)
	if err != nil {
		t.Fatalf("ordinary frames: %v", err)
	}
	if len(ordinary) != 3 {
		t.Fatalf("ordinary frame count = %d, want 3", len(ordinary))
	}
	aggregates, err := timescaledb.NewFrameQuery[integrationAggregateFrame](source, "observed_at").
		Bucket(timescaledb.FixedInterval(15*time.Second)).
		Dimension("series_id").
		Measure(
			timescaledb.Count("value").As("sample_count"),
			timescaledb.Sum("value").As("total"),
			timescaledb.Avg("value").As("average"),
			timescaledb.Min("value").As("minimum"),
			timescaledb.Max("value").As("maximum"),
			timescaledb.First("value", "observed_at").As("first_value"),
			timescaledb.Last("value", "observed_at").As("last_value"),
		).
		Between(base, base.Add(time.Minute)).
		Where(
			timescaledb.Equal("series_id", "alpha"),
			timescaledb.NotEqual("series_id", "beta"),
			timescaledb.LessThan("value", 100.0),
			timescaledb.LessThanOrEqual("value", 100.0),
			timescaledb.GreaterThan("value", 0.0),
			timescaledb.GreaterThanOrEqual("value", 10.0),
			timescaledb.In("series_id", "alpha", "gamma"),
			timescaledb.NotIn("series_id", "beta", "gamma"),
			timescaledb.IsNotNull("value"),
		).
		OrderDescending().
		Execute(ctx, db)
	if err != nil {
		t.Fatalf("all typed aggregates and predicates: %v", err)
	}
	if len(aggregates) != 3 || !aggregates[0].Bucket.Equal(base.Add(45*time.Second)) {
		t.Fatalf("descending aggregate frames = %+v", aggregates)
	}
	firstBucket := aggregates[2]
	if firstBucket.SampleCount != 2 || firstBucket.Total != 30 || firstBucket.Average != 15 ||
		firstBucket.Minimum != 10 || firstBucket.Maximum != 20 || firstBucket.First != 10 || firstBucket.Last != 20 {
		t.Fatalf("typed aggregate values = %+v", firstBucket)
	}
	nullPredicate, err := timescaledb.NewFrameQuery[integrationFrame](source, "observed_at").
		Bucket(timescaledb.FixedInterval(15*time.Second)).
		Dimension("series_id").Measure(timescaledb.Avg("value").As("average")).
		Between(base, base.Add(time.Minute)).Where(timescaledb.IsNull("value")).
		Execute(ctx, db)
	if err != nil || nullPredicate == nil || len(nullPredicate) != 0 {
		t.Fatalf("IS NULL frames = %#v, %v", nullPredicate, err)
	}
	calendarFrames, err := timescaledb.NewFrameQuery[integrationFrame](source, "observed_at").
		Bucket(timescaledb.CalendarMonths(1)).Timezone("UTC").
		Dimension("series_id").Measure(timescaledb.Avg("value").As("average")).
		Between(base, base.AddDate(0, 1, 0)).Where(timescaledb.Equal("series_id", "alpha")).
		Execute(ctx, db)
	if err != nil || len(calendarFrames) != 1 {
		t.Fatalf("calendar frame = %+v, %v", calendarFrames, err)
	}
	alignedFrames, err := timescaledb.NewFrameQuery[integrationFrame](source, "observed_at").
		Bucket(timescaledb.FixedInterval(15*time.Second)).
		Timezone("UTC").Origin(base).Offset(timescaledb.FixedInterval(5*time.Second)).
		Dimension("series_id").Measure(timescaledb.Avg("value").As("average")).
		Between(base, base.Add(time.Minute)).Where(timescaledb.Equal("series_id", "alpha")).
		Execute(ctx, db)
	if err != nil || len(alignedFrames) == 0 {
		t.Fatalf("timezone/origin/offset aligned frames = %+v, %v", alignedFrames, err)
	}
	_, err = timescaledb.NewFrameQuery[integrationFrame](source, "observed_at").
		Bucket(timescaledb.FixedInterval(15*time.Second)).
		Dimension("series_id").
		Measure(timescaledb.TrustedMeasure(timescaledb.TrustedExpression(`pgxext_missing_aggregate(value)`)).As("average")).
		Between(base, base.Add(time.Minute)).
		Execute(ctx, db)
	if err == nil {
		t.Fatal("expected PostgreSQL error from missing aggregate")
	}
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) {
		t.Fatalf("PostgreSQL error was not preserved: %v", err)
	}
	canceledContext, cancelQuery := context.WithCancel(ctx)
	cancelQuery()
	_, err = timescaledb.NewFrameQuery[integrationFrame](source, "observed_at").
		Bucket(timescaledb.FixedInterval(15*time.Second)).
		Dimension("series_id").Measure(timescaledb.Avg("value").As("average")).
		Between(base, base.Add(time.Minute)).Execute(canceledContext, db)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("context cancellation was not preserved: %v", err)
	}
	empty, err := timescaledb.NewFrameQuery[integrationFrame](source, "observed_at").
		Bucket(timescaledb.FixedInterval(15*time.Second)).
		Dimension("series_id").Measure(timescaledb.Avg("value").As("average")).
		Between(base, base.Add(time.Minute)).Where(timescaledb.Equal("series_id", "missing")).
		Execute(ctx, db)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("empty frames = %#v, %v", empty, err)
	}

	nullGaps, err := timescaledb.NewFrameQuery[integrationFrame](source, "observed_at").
		Bucket(timescaledb.FixedInterval(15*time.Second)).Timezone("UTC").GapFill().
		Dimension("series_id").Measure(timescaledb.Avg("value").As("average")).
		Between(base, base.Add(time.Minute)).Where(timescaledb.Equal("series_id", "alpha")).
		Execute(ctx, db)
	if err != nil {
		t.Fatalf("null gapfill: %v", err)
	}
	if len(nullGaps) != 4 || nullGaps[1].Average != nil {
		t.Fatalf("null gapfill = %+v", nullGaps)
	}

	locf, err := timescaledb.NewFrameQuery[integrationFrame](source, "observed_at").
		Bucket(timescaledb.FixedInterval(15*time.Second)).
		Dimension("series_id").
		Measure(timescaledb.Avg("value").As("average").LOCF(true).
			PreviousLookup(timescaledb.TrustedExpression(`SELECT 5.0::double precision`))).
		Between(base, base.Add(time.Minute)).Where(timescaledb.Equal("series_id", "alpha")).
		Execute(ctx, db)
	if err != nil {
		t.Fatalf("LOCF: %v", err)
	}
	if len(locf) != 4 || locf[1].Average == nil || math.Abs(*locf[1].Average-15) > 1e-9 {
		t.Fatalf("LOCF frames = %+v", locf)
	}

	interpolated, err := timescaledb.NewFrameQuery[integrationFrame](source, "observed_at").
		Bucket(timescaledb.FixedInterval(15*time.Second)).
		Dimension("series_id").
		Measure(timescaledb.Avg("value").As("average").Interpolate().
			PreviousLookup(timescaledb.TrustedExpression(`SELECT (TIMESTAMPTZ '2024-12-31 23:59:45+00', 0.0::double precision)`)).
			NextLookup(timescaledb.TrustedExpression(`SELECT (TIMESTAMPTZ '2025-01-01 00:01:00+00', 50.0::double precision)`))).
		Between(base, base.Add(time.Minute)).Where(timescaledb.Equal("series_id", "alpha")).
		Execute(ctx, db)
	if err != nil {
		t.Fatalf("interpolate: %v", err)
	}
	if len(interpolated) != 4 || interpolated[1].Average == nil || math.Abs(*interpolated[1].Average-22.5) > 1e-9 {
		t.Fatalf("interpolated frames = %+v", interpolated)
	}

	view := timescaledb.Relation{Schema: integrationSchema, Name: "observations_15s"}
	aggregate := timescaledb.NewContinuousAggregate(view, source, "observed_at").
		Bucket(timescaledb.FixedInterval(15 * time.Second)).
		Dimension("series_id").
		Measure(timescaledb.Avg("value").As("average")).
		WithNoData()
	if err := aggregate.Apply(ctx, db); err != nil {
		t.Fatalf("create continuous aggregate: %v", err)
	}
	if err := aggregate.IfNotExists().Apply(ctx, db); err != nil {
		t.Fatalf("idempotent continuous aggregate create: %v", err)
	}
	isAggregate, err := timescaledb.IsContinuousAggregate(ctx, db, view)
	if err != nil || !isAggregate {
		t.Fatalf("IsContinuousAggregate = %v, %v", isAggregate, err)
	}
	continuousAggregates, err := timescaledb.ListContinuousAggregates(ctx, db)
	if err != nil || !containsContinuousAggregate(continuousAggregates, view) {
		t.Fatalf("ListContinuousAggregates = %+v, %v", continuousAggregates, err)
	}
	caggMetadata, err := timescaledb.GetContinuousAggregate(ctx, db, view)
	if err != nil || !caggMetadata.MaterializedOnly {
		t.Fatalf("continuous aggregate metadata = %+v, %v", caggMetadata, err)
	}
	if err := aggregate.Refresh(ctx, db, base, base.Add(45*time.Second), false); err != nil {
		t.Fatalf("manual refresh: %v", err)
	}

	materialized := readAggregateFrames(t, ctx, db, view)
	if len(materialized) != 2 {
		t.Fatalf("materialized-only rows = %+v, want two closed non-empty buckets", materialized)
	}
	if err := aggregate.SetRealTime(ctx, db, true); err != nil {
		t.Fatalf("enable real-time: %v", err)
	}
	realTime := readAggregateFrames(t, ctx, db, view)
	if len(realTime) != 3 {
		t.Fatalf("real-time rows = %+v, want recent raw bucket", realTime)
	}
	if err := aggregate.SetRealTime(ctx, db, false); err != nil {
		t.Fatalf("disable real-time: %v", err)
	}
	caggMetadata, err = timescaledb.GetContinuousAggregate(ctx, db, view)
	if err != nil || !caggMetadata.MaterializedOnly || len(readAggregateFrames(t, ctx, db, view)) != 2 {
		t.Fatalf("disabled real-time metadata = %+v, %v", caggMetadata, err)
	}
	if err := aggregate.SetRealTime(ctx, db, true); err != nil {
		t.Fatalf("re-enable real-time: %v", err)
	}

	// Real-time mode does not pick up historical rows before the watermark.
	if _, err := db.Exec(ctx, `INSERT INTO `+integrationSchema+`.observations (observed_at, series_id, value) VALUES ($1, $2, $3)`, base.Add(5*time.Second), "alpha", 30.0); err != nil {
		t.Fatalf("insert historical row: %v", err)
	}
	beforeRefresh := readAggregateFrames(t, ctx, db, view)
	if beforeRefresh[0].Average == nil || math.Abs(*beforeRefresh[0].Average-15) > 1e-9 {
		t.Fatalf("historical row unexpectedly visible before refresh: %+v", beforeRefresh)
	}
	if err := aggregate.Refresh(ctx, db, base, base.Add(15*time.Second), true); err != nil {
		t.Fatalf("force historical refresh: %v", err)
	}
	afterRefresh := readAggregateFrames(t, ctx, db, view)
	if afterRefresh[0].Average == nil || math.Abs(*afterRefresh[0].Average-20) > 1e-9 {
		t.Fatalf("historical row missing after refresh: %+v", afterRefresh)
	}

	// Gapfill may be applied while querying a continuous aggregate.
	fromCagg, err := timescaledb.NewFrameQuery[integrationFrame](aggregate.Relation(), "bucket").
		Bucket(timescaledb.FixedInterval(15*time.Second)).Dimension("series_id").
		Measure(timescaledb.Last("average", "bucket").As("average").LOCF(true)).
		Between(base, base.Add(time.Minute)).Where(timescaledb.Equal("series_id", "alpha")).
		Execute(ctx, db)
	if err != nil || len(fromCagg) != 4 {
		t.Fatalf("gapfill continuous aggregate = %+v, %v", fromCagg, err)
	}

	rollupView := timescaledb.Relation{Schema: integrationSchema, Name: "observations_30s"}
	rollup := timescaledb.NewContinuousAggregate(rollupView, view, "bucket").
		Bucket(timescaledb.FixedInterval(30 * time.Second)).
		Dimension("series_id").
		Measure(timescaledb.Avg("average").As("average")).
		CreateGroupIndexes(false).
		WithData()
	if err := rollup.Apply(ctx, db); err != nil {
		t.Fatalf("create hierarchical continuous aggregate: %v", err)
	}
	if prepopulated := readAggregateFrames(t, ctx, db, rollupView); len(prepopulated) == 0 {
		t.Fatal("hierarchical continuous aggregate WITH DATA returned no rows")
	}
	if err := rollup.Refresh(ctx, db, base, base.Add(30*time.Second), false); err != nil {
		t.Fatalf("refresh hierarchical continuous aggregate: %v", err)
	}
	rollupFrames := readAggregateFrames(t, ctx, db, rollupView)
	if !containsFrameValue(rollupFrames, base, 20) {
		t.Fatalf("hierarchical continuous aggregate = %+v", rollupFrames)
	}
	if err := rollup.Drop(ctx, db, false); err != nil {
		t.Fatalf("drop hierarchical continuous aggregate: %v", err)
	}
	if err := rollup.Drop(ctx, db, true); err != nil {
		t.Fatalf("drop missing hierarchical continuous aggregate: %v", err)
	}

	policy, err := timescaledb.PlanRefreshPolicy(15*time.Second, 10*time.Minute, 15*time.Second)
	if err != nil {
		t.Fatalf("PlanRefreshPolicy: %v", err)
	}
	includeTieredData := false
	policy.IncludeTieredData = &includeTieredData
	policy.BucketsPerBatch = 2
	policy.MaxBatchesPerExecution = 3
	policy.Timezone = "UTC"
	initialStart := time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
	policy.InitialStart = &initialStart
	refreshJobID, err := timescaledb.AddRefreshPolicy(ctx, db, view, policy)
	if err != nil || refreshJobID <= 0 {
		t.Fatalf("AddRefreshPolicy = %d, %v", refreshJobID, err)
	}
	inspected, err := timescaledb.InspectRefreshPolicy(ctx, db, view)
	if err != nil || !timescaledb.RefreshPoliciesEqual(inspected.Policy, policy) {
		t.Fatalf("inspect refresh policy = %+v, %v", inspected, err)
	}
	action, err := aggregate.EnsureRefreshPolicy(ctx, db, policy)
	if err != nil || action != timescaledb.PolicyUnchanged {
		t.Fatalf("ensure unchanged refresh policy = %q, %v", action, err)
	}
	drifted := policy
	drifted.ScheduleInterval = timescaledb.FixedInterval(30 * time.Second)
	action, err = aggregate.EnsureRefreshPolicy(ctx, db, drifted)
	if !errors.Is(err, timescaledb.ErrPolicyDrift) || action != timescaledb.PolicyUnchanged {
		t.Fatalf("ensure drifted refresh policy = %q, %v", action, err)
	}
	action, err = aggregate.ReconcileRefreshPolicy(ctx, db, drifted)
	if err != nil || action != timescaledb.PolicyReplaced {
		t.Fatalf("reconcile refresh policy = %q, %v", action, err)
	}
	replacementRefresh := drifted
	replacementRefresh.ScheduleInterval = timescaledb.FixedInterval(45 * time.Second)
	action, err = timescaledb.ReplaceRefreshPolicy(ctx, db, view, replacementRefresh)
	if err != nil || action != timescaledb.PolicyReplaced {
		t.Fatalf("ReplaceRefreshPolicy = %q, %v", action, err)
	}
	action, err = timescaledb.ReconcileRefreshPolicy(ctx, db, view, replacementRefresh)
	if err != nil || action != timescaledb.PolicyUnchanged {
		t.Fatalf("noop ReconcileRefreshPolicy = %q, %v", action, err)
	}
	job, stats, err := timescaledb.GetPolicyJob(ctx, db, view, timescaledb.RefreshPolicyKind)
	if err != nil || job == nil || job.ID <= 0 || job.ApplicationName == "" || stats == nil || stats.JobID != job.ID {
		t.Fatalf("GetPolicyJob = %+v, %+v, %v", job, stats, err)
	}
	jobs, err := timescaledb.ListJobs(ctx, db)
	if err != nil || !containsJob(jobs, job.ID) {
		t.Fatalf("ListJobs = %+v, %v", jobs, err)
	}
	directJob, err := timescaledb.GetJob(ctx, db, job.ID)
	if err != nil || directJob == nil || directJob.ID != job.ID {
		t.Fatalf("GetJob = %+v, %v", directJob, err)
	}
	directStats, err := timescaledb.GetJobStats(ctx, db, job.ID)
	if err != nil || directStats == nil || directStats.JobID != job.ID {
		t.Fatalf("GetJobStats = %+v, %v", directStats, err)
	}
	if err := timescaledb.RemoveRefreshPolicy(ctx, db, view, true); err != nil {
		t.Fatalf("RemoveRefreshPolicy: %v", err)
	}
	if _, err := timescaledb.InspectRefreshPolicy(ctx, db, view); !errors.Is(err, timescaledb.ErrPolicyNotFound) {
		t.Fatalf("removed refresh policy error = %v", err)
	}

	retention := timescaledb.NewRetentionPolicy(timescaledb.FixedInterval(90 * 24 * time.Hour))
	retention.InitialStart = &initialStart
	retention.Timezone = "UTC"
	retentionJobID, err := timescaledb.AddRetentionPolicy(ctx, db, source, retention)
	if err != nil || retentionJobID <= 0 {
		t.Fatalf("AddRetentionPolicy = %d, %v", retentionJobID, err)
	}
	retentionMetadata, err := timescaledb.InspectRetentionPolicy(ctx, db, source)
	if err != nil || !timescaledb.RetentionPoliciesEqual(retentionMetadata.Policy, retention) {
		t.Fatalf("inspect retention policy = %+v, %v", retentionMetadata, err)
	}
	action, err = timescaledb.EnsureRetentionPolicy(ctx, db, source, retention)
	if err != nil || action != timescaledb.PolicyUnchanged {
		t.Fatalf("ensure unchanged retention policy = %q, %v", action, err)
	}
	retentionDrift := retention
	retentionDrift.ScheduleInterval = timescaledb.FixedInterval(12 * time.Hour)
	action, err = timescaledb.EnsureRetentionPolicy(ctx, db, source, retentionDrift)
	if !errors.Is(err, timescaledb.ErrPolicyDrift) || action != timescaledb.PolicyUnchanged {
		t.Fatalf("ensure drifted retention policy = %q, %v", action, err)
	}
	action, err = timescaledb.ReconcileRetentionPolicy(ctx, db, source, retentionDrift)
	if err != nil || action != timescaledb.PolicyReplaced {
		t.Fatalf("reconcile retention policy = %q, %v", action, err)
	}
	retentionReplacement := retentionDrift
	retentionReplacement.ScheduleInterval = timescaledb.FixedInterval(18 * time.Hour)
	action, err = timescaledb.ReplaceRetentionPolicy(ctx, db, source, retentionReplacement)
	if err != nil || action != timescaledb.PolicyReplaced {
		t.Fatalf("ReplaceRetentionPolicy = %q, %v", action, err)
	}
	action, err = timescaledb.ReconcileRetentionPolicy(ctx, db, source, retentionReplacement)
	if err != nil || action != timescaledb.PolicyUnchanged {
		t.Fatalf("noop ReconcileRetentionPolicy = %q, %v", action, err)
	}
	retentionJob, retentionStats, err := timescaledb.GetPolicyJob(ctx, db, source, timescaledb.RetentionPolicyKind)
	if err != nil || retentionJob == nil || retentionStats == nil || retentionStats.JobID != retentionJob.ID {
		t.Fatalf("retention GetPolicyJob = %+v, %+v, %v", retentionJob, retentionStats, err)
	}
	if err := timescaledb.RemoveRetentionPolicy(ctx, db, source, true); err != nil {
		t.Fatalf("RemoveRetentionPolicy: %v", err)
	}
	if _, err := timescaledb.InspectRetentionPolicy(ctx, db, source); !errors.Is(err, timescaledb.ErrPolicyNotFound) {
		t.Fatalf("removed retention policy error = %v", err)
	}

	columnstoreSettings := timescaledb.NewColumnstoreSettings().
		SegmentBy("series_id").
		OrderBy(timescaledb.ColumnstoreDescending("observed_at"))
	if err := timescaledb.EnableHypertableColumnstore(ctx, db, source, columnstoreSettings); err != nil {
		t.Fatalf("EnableHypertableColumnstore: %v", err)
	}
	metadata, err = timescaledb.GetHypertable(ctx, db, source)
	if err != nil || !metadata.ColumnstoreEnabled {
		t.Fatalf("enabled hypertable columnstore metadata = %+v, %v", metadata, err)
	}
	columnstore := timescaledb.NewColumnstorePolicy(timescaledb.FixedInterval(30 * 24 * time.Hour))
	columnstore.InitialStart = &initialStart
	columnstore.Timezone = "UTC"
	if err := timescaledb.AddColumnstorePolicy(ctx, db, source, columnstore); err != nil {
		t.Fatalf("AddColumnstorePolicy: %v", err)
	}
	columnstoreMetadata, err := timescaledb.InspectColumnstorePolicy(ctx, db, source)
	if err != nil || !timescaledb.ColumnstorePoliciesEqual(columnstoreMetadata.Policy, columnstore) {
		t.Fatalf("inspect columnstore policy = %+v, %v", columnstoreMetadata, err)
	}
	action, err = timescaledb.EnsureColumnstorePolicy(ctx, db, source, columnstore)
	if err != nil || action != timescaledb.PolicyUnchanged {
		t.Fatalf("ensure unchanged columnstore policy = %q, %v", action, err)
	}
	columnstoreDrift := columnstore
	columnstoreDrift.ScheduleInterval = timescaledb.FixedInterval(6 * time.Hour)
	action, err = timescaledb.EnsureColumnstorePolicy(ctx, db, source, columnstoreDrift)
	if !errors.Is(err, timescaledb.ErrPolicyDrift) || action != timescaledb.PolicyUnchanged {
		t.Fatalf("ensure drifted columnstore policy = %q, %v", action, err)
	}
	action, err = timescaledb.ReconcileColumnstorePolicy(ctx, db, source, columnstoreDrift)
	if err != nil || action != timescaledb.PolicyReplaced {
		t.Fatalf("reconcile columnstore policy = %q, %v", action, err)
	}
	columnstoreReplacement := columnstoreDrift
	columnstoreReplacement.ScheduleInterval = timescaledb.FixedInterval(9 * time.Hour)
	action, err = timescaledb.ReplaceColumnstorePolicy(ctx, db, source, columnstoreReplacement)
	if err != nil || action != timescaledb.PolicyReplaced {
		t.Fatalf("ReplaceColumnstorePolicy = %q, %v", action, err)
	}
	action, err = timescaledb.ReconcileColumnstorePolicy(ctx, db, source, columnstoreReplacement)
	if err != nil || action != timescaledb.PolicyUnchanged {
		t.Fatalf("noop ReconcileColumnstorePolicy = %q, %v", action, err)
	}
	columnstoreJob, columnstoreStats, err := timescaledb.GetPolicyJob(ctx, db, source, timescaledb.ColumnstorePolicyKind)
	if err != nil || columnstoreJob == nil || columnstoreStats == nil || columnstoreStats.JobID != columnstoreJob.ID {
		t.Fatalf("columnstore GetPolicyJob = %+v, %+v, %v", columnstoreJob, columnstoreStats, err)
	}
	if err := timescaledb.RemoveColumnstorePolicy(ctx, db, source, true); err != nil {
		t.Fatalf("RemoveColumnstorePolicy: %v", err)
	}
	if _, err := timescaledb.InspectColumnstorePolicy(ctx, db, source); !errors.Is(err, timescaledb.ErrPolicyNotFound) {
		t.Fatalf("removed columnstore policy error = %v", err)
	}
	if err := timescaledb.DisableHypertableColumnstore(ctx, db, source); err != nil {
		t.Fatalf("DisableHypertableColumnstore: %v", err)
	}
	metadata, err = timescaledb.GetHypertable(ctx, db, source)
	if err != nil || metadata.ColumnstoreEnabled {
		t.Fatalf("disabled hypertable columnstore metadata = %+v, %v", metadata, err)
	}

	caggColumnstore := timescaledb.NewColumnstoreSettings().
		SegmentBy("series_id").
		OrderBy(timescaledb.ColumnstoreAscending("bucket").NullsFirst())
	if err := timescaledb.EnableContinuousAggregateColumnstore(ctx, db, view, caggColumnstore); err != nil {
		t.Fatalf("EnableContinuousAggregateColumnstore: %v", err)
	}
	caggMetadata, err = timescaledb.GetContinuousAggregate(ctx, db, view)
	if err != nil || !caggMetadata.ColumnstoreEnabled {
		t.Fatalf("enabled continuous aggregate columnstore metadata = %+v, %v", caggMetadata, err)
	}
	if err := timescaledb.DisableContinuousAggregateColumnstore(ctx, db, view); err != nil {
		t.Fatalf("DisableContinuousAggregateColumnstore: %v", err)
	}
	caggMetadata, err = timescaledb.GetContinuousAggregate(ctx, db, view)
	if err != nil || caggMetadata.ColumnstoreEnabled {
		t.Fatalf("disabled continuous aggregate columnstore metadata = %+v, %v", caggMetadata, err)
	}

	directRelation := timescaledb.Relation{Schema: integrationSchema, Name: "direct_observations"}
	directTable := timescaledb.NewHypertableTable(
		directRelation,
		"observed_at",
		timescaledb.TrustedExpression(`observed_at TIMESTAMPTZ NOT NULL, series_id TEXT NOT NULL, value DOUBLE PRECISION`),
	).
		IfNotExists().
		CreateDefaultIndexes(false).
		ChunkInterval(timescaledb.FixedInterval(time.Hour)).
		Columnstore(timescaledb.NewColumnstoreSettings().
			SegmentBy("series_id").
			OrderBy(timescaledb.ColumnstoreDescending("observed_at").NullsLast()))
	if err := directTable.Apply(ctx, db); err != nil {
		t.Fatalf("modern hypertable Apply: %v", err)
	}
	if err := directTable.Apply(ctx, db); err != nil {
		t.Fatalf("idempotent modern hypertable Apply: %v", err)
	}
	directMetadata, err := timescaledb.GetHypertable(ctx, db, directRelation)
	if err != nil || !directMetadata.ColumnstoreEnabled || directMetadata.PrimaryDimension != "observed_at" {
		t.Fatalf("modern hypertable metadata = %+v, %v", directMetadata, err)
	}
	if _, err := db.Exec(ctx, `DROP TABLE `+integrationSchema+`.direct_observations`); err != nil {
		t.Fatalf("drop directly applied hypertable: %v", err)
	}

	// Generated modern DDL is accepted by the repository's transactional migrator.
	migrationRelation := timescaledb.Relation{Schema: integrationSchema, Name: "migration_observations"}
	modern := timescaledb.NewHypertableTable(
		migrationRelation,
		"observed_at",
		timescaledb.TrustedExpression(`observed_at TIMESTAMPTZ NOT NULL, series_id TEXT NOT NULL, value DOUBLE PRECISION`),
	).ChunkInterval(timescaledb.FixedInterval(time.Hour))
	upSQL, err := modern.BuildSQL()
	if err != nil {
		t.Fatalf("modern BuildSQL: %v", err)
	}
	const migrationName = "pgxext_timescaledb_integration_modern_hypertable"
	_, _ = db.Exec(ctx, `DELETE FROM migrations WHERE name = $1`, migrationName)
	migrator := migration.NewMigrator(ctx, db)
	set := migration.MigrationSet{{
		Name: migrationName, UpQuery: upSQL,
		DownQuery: `DROP TABLE ` + integrationSchema + `.migration_observations`,
	}}
	if err := migrator.Up(set); err != nil {
		t.Fatalf("migrator Up generated DDL: %v", err)
	}
	if ok, err := timescaledb.IsHypertable(ctx, db, migrationRelation); err != nil || !ok {
		t.Fatalf("generated migration hypertable = %v, %v", ok, err)
	}
	if err := migrator.Down(set); err != nil {
		t.Fatalf("migrator Down generated DDL: %v", err)
	}
	if err := aggregate.Drop(ctx, db, false); err != nil {
		t.Fatalf("drop continuous aggregate: %v", err)
	}
	if err := aggregate.Drop(ctx, db, true); err != nil {
		t.Fatalf("drop missing continuous aggregate: %v", err)
	}
	isAggregate, err = timescaledb.IsContinuousAggregate(ctx, db, view)
	if err != nil || isAggregate {
		t.Fatalf("dropped IsContinuousAggregate = %v, %v", isAggregate, err)
	}
}

func assertTimescalePostgreSQLMajor(t *testing.T, ctx context.Context, db *pgxext.DataSource) {
	t.Helper()
	expectedText := os.Getenv("TEST_TIMESCALE_POSTGRES_MAJOR")
	if expectedText == "" {
		if os.Getenv("PGXEXT_REQUIRE_INTEGRATION") == "1" {
			t.Fatal("TEST_TIMESCALE_POSTGRES_MAJOR not set while integration tests are required")
		}
		return
	}
	expected, err := strconv.Atoi(expectedText)
	if err != nil {
		t.Fatalf("invalid TEST_TIMESCALE_POSTGRES_MAJOR %q: %v", expectedText, err)
	}
	rows, err := db.Query(ctx, `SELECT current_setting('server_version_num')::integer / 10000`)
	if err != nil {
		t.Fatalf("query TimescaleDB PostgreSQL major: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatalf("query TimescaleDB PostgreSQL major returned no row: %v", rows.Err())
	}
	var actual int
	if err := rows.Scan(&actual); err != nil {
		t.Fatalf("scan TimescaleDB PostgreSQL major: %v", err)
	}
	if actual != expected {
		t.Fatalf("TimescaleDB PostgreSQL major = %d, want %d", actual, expected)
	}
}

func containsHypertable(items []timescaledb.HypertableMetadata, relation timescaledb.Relation) bool {
	for _, item := range items {
		if item.Relation == relation {
			return true
		}
	}
	return false
}

func containsDimension(items []timescaledb.DimensionMetadata, relation timescaledb.Relation, column timescaledb.Column) bool {
	for _, item := range items {
		if item.Relation == relation && item.Column == column {
			return true
		}
	}
	return false
}

func containsContinuousAggregate(items []timescaledb.ContinuousAggregateMetadata, relation timescaledb.Relation) bool {
	for _, item := range items {
		if item.View == relation {
			return true
		}
	}
	return false
}

func containsJob(items []timescaledb.JobMetadata, jobID int64) bool {
	for _, item := range items {
		if item.ID == jobID {
			return true
		}
	}
	return false
}

func containsFrameValue(items []*integrationFrame, bucket time.Time, value float64) bool {
	for _, item := range items {
		if item.Bucket.Equal(bucket) && item.Average != nil && math.Abs(*item.Average-value) <= 1e-9 {
			return true
		}
	}
	return false
}

func readAggregateFrames(t *testing.T, ctx context.Context, db *pgxext.DataSource, relation timescaledb.Relation) []*integrationFrame {
	t.Helper()
	rows, err := db.Query(ctx, `SELECT bucket, series_id, average FROM `+relation.String()+` ORDER BY bucket, series_id`)
	if err != nil {
		t.Fatalf("query aggregate %s: %v", relation.String(), err)
	}
	result, err := pgx.CollectRows(rows, pgx.RowToAddrOfStructByName[integrationFrame])
	if err != nil {
		t.Fatalf("collect aggregate %s: %v", relation.String(), err)
	}
	return result
}
