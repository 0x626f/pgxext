# timescaledb

`timescaledb` is a generic SQL capability layer for TimescaleDB and pgx/v5. It
builds and applies Timescale-specific definitions, constructs typed frame
queries, controls policies and refreshes, and reads operational metadata. It is
not an ORM and does not own a connection pool.

## Compatibility

| Component | Supported baseline |
| --- | --- |
| TimescaleDB | `>= 2.20.0` and `< 3.0.0` |
| PostgreSQL with TimescaleDB 2.20.x | 15, 16, or 17 |
| PostgreSQL 18 | TimescaleDB `>= 2.23.0` |
| Go driver | pgx/v5 |
| Time dimension | `TIMESTAMPTZ` |
| Integer time dimensions | Not supported in this release |

TimescaleDB 2.20 is the baseline because modern `CREATE TABLE ... WITH
(tsdb.hypertable, ...)` was introduced there. The generalized
`create_hypertable` API remains available for converting an existing table.
Columnstore procedures are stable from TimescaleDB 2.18 and are version-gated
by this package at the 2.20 package baseline. PostgreSQL majors follow
Timescale's [official support
matrix](https://docs.timescale.com/self-hosted/latest/upgrades/major-upgrade/).
The package does not expose the former hypercore table-access-method policy
option; TimescaleDB removed that experimental access method in 2.22. Stable
rowstore-to-columnstore policies work across the supported matrix.

Hypertables and basic time bucketing are core extension features. Continuous
aggregates, real-time aggregation, refresh policies, retention, gapfill, and
columnstore availability depend on the installed Timescale edition/version.
Timescale Community Edition or Timescale Cloud is required for the lifecycle
features marked Community in the [official edition
matrix](https://docs.timescale.com/about/latest/timescaledb-editions/).

## Extension setup

Installation is always explicit and belongs in an application migration or
authorized bootstrap:

```go
upSQL := timescaledb.CreateExtensionSQL()
// CREATE EXTENSION IF NOT EXISTS timescaledb;
```

Or execute it directly:

```go
if err := timescaledb.CreateExtension(ctx, db); err != nil {
    return err
}
```

Use `ExtensionInstalled`, `InstalledVersion`, and `InspectCapabilities` to
inspect the current database. Capability inspection checks extension-member
functions for gapfill, continuous aggregates, refresh, retention, and
columnstore instead of assuming that every edition exposes them. Constructors
never connect or install anything.
There is deliberately no automatic `DROP EXTENSION` rollback because a cascade
could destroy unrelated application data.

## Hypertables

Applications own columns, indexes, constraints, and ordinary writes. Convert an
existing application table after creating it:

```go
source := timescaledb.Relation{
    Schema: "metrics",
    Name:   "observations",
}

hypertable := timescaledb.NewHypertable(source, "observed_at").
    ChunkInterval(timescaledb.FixedInterval(24 * time.Hour)).
    IfNotExists()

if err := hypertable.Apply(ctx, db); err != nil {
    return err
}
```

`MigrateExistingData` opts into Timescale's potentially long table lock for a
non-empty table. `HashDimension(column, partitions)` is opt-in and rejects
non-positive partition counts. Additional hash dimensions are normally useful
only for parallel I/O across multiple tablespaces; they should not be the
default.

For a new table, the package supports TimescaleDB 2.20's modern syntax, which
creates the hypertable with columnstore enabled. Optional `Columnstore`
settings configure safe segment-by and order-by identifiers. Column definitions
are intentionally a `TrustedExpression` because the application owns its
schema:

```go
table := timescaledb.NewHypertableTable(
    source,
    "observed_at",
    timescaledb.TrustedExpression(`
        observed_at TIMESTAMPTZ NOT NULL,
        series_id   TEXT NOT NULL,
        value       DOUBLE PRECISION NOT NULL
    `),
).ChunkInterval(timescaledb.FixedInterval(24 * time.Hour))

sql, err := table.BuildSQL() // suitable for an application migration
```

Trusted expressions must be static application code, never request input.
Modern `CREATE TABLE` creates the primary dimension; an optional hash dimension
is emitted as the documented stable `add_dimension(..., by_hash(...))` call.

`SetChunkInterval` and `SetChunkIntervalSQL` affect only chunks created after
the change. Existing chunks keep their original ranges. Conversion is not
presented as automatically reversible; the application migration decides how
to roll back or migrate the underlying table.

## Fifteen-second frames

Models use pgx row-to-struct-by-name conventions:

```go
type Frame struct {
    Bucket   time.Time `db:"bucket"`
    SeriesID string    `db:"series_id"`
    Average  *float64  `db:"average"`
}

frames, err := timescaledb.NewFrameQuery[Frame](source, "observed_at").
    Bucket(timescaledb.FixedInterval(15 * time.Second)).
    Dimension("series_id").
    Measure(timescaledb.Avg("value").As("average")).
    Between(from, to).
    Where(timescaledb.Equal("series_id", seriesID)).
    OrderAscending().
    Execute(ctx, db)
```

The range is required and half-open: `observed_at >= from AND observed_at <
to`. The predicate remains in `WHERE` for chunk pruning. Runtime values are pgx
parameters; relation, column, and aliases are quoted identifiers. `Build`
returns SQL and arguments without using a database.

Safe measures include `Count`, `Sum`, `Avg`, `Min`, `Max`, `First(value,
time)`, and `Last(value, time)`. Advanced static aggregates use
`TrustedMeasure`. Duplicate dimensions and output aliases are rejected.

`Timezone`, `Origin`, and `Offset` expose documented `time_bucket` alignment.
Without the timezone overload, origin and offset cannot be combined. Month
bucket widths use `CalendarMonths`; month widths cannot be mixed with fixed
components.

## Gap filling

Missing buckets with null measures:

```go
frames, err := timescaledb.NewFrameQuery[Frame](source, "observed_at").
    Bucket(timescaledb.FixedInterval(15 * time.Second)).
    GapFill().
    Dimension("series_id").
    Measure(timescaledb.Avg("value").As("average")).
    Between(from, to).
    Execute(ctx, db)
```

LOCF and numeric interpolation are selected per measure:

```go
timescaledb.Avg("value").As("average").LOCF(true)
timescaledb.Avg("value").As("average").Interpolate()
```

The LOCF boolean maps to `treat_null_as_missing`. Optional previous and next
lookup expressions require `TrustedExpression`; interpolation lookups return
`(time,value)` rows and LOCF's previous lookup returns a scalar. Interpolation
works only with compatible Timescale numeric types.

Gapfill always requires a bounded range and keeps the time predicate in
`WHERE`. It supports timezone alignment but not origin or offset. The package
emits `time_bucket_gapfill` as a top-level expression. It rejects gapfill in a
continuous-aggregate definition; apply gapfill when querying the aggregate.

## Continuous aggregates and real-time reads

```go
aggregate := timescaledb.NewContinuousAggregate(
    timescaledb.Relation{Schema: "metrics", Name: "observations_15s"},
    source,
    "observed_at",
).
    Bucket(timescaledb.FixedInterval(15 * time.Second)).
    Dimension("series_id").
    Measure(timescaledb.Avg("value").As("average")).
    WithNoData().
    RealTime(true)

if err := aggregate.Apply(ctx, db); err != nil {
    return err
}
```

Safe defaults are `WITH NO DATA`, `timescaledb.create_group_indexes=true`, and
`timescaledb.materialized_only=true`. `RealTime(true)` explicitly changes
`materialized_only` to `false`. `IfNotExists` suppresses a name collision only;
it does not reconcile a changed SELECT definition. The package does not create
unique continuous-aggregate indexes because Timescale does not support them.

Change an existing view at runtime:

```go
err := timescaledb.SetRealTime(ctx, db, aggregate.Relation(), true)
```

Real-time aggregation combines materialized history with raw rows newer than
the materialization watermark at query time. It is not a push or streaming
mechanism. A historical row inserted before the watermark appears only after a
policy run or manual refresh.

## Refresh planning, policies, and manual refresh

For fixed-width UTC buckets, derive a late-arrival-aware policy:

```go
policy, err := timescaledb.PlanRefreshPolicy(
    15*time.Second, // bucket width
    10*time.Minute, // application-supplied late-arrival/reorg tolerance
    0,              // defaults to bucket width
)
if err != nil {
    return err
}

action, err := aggregate.ReconcileRefreshPolicy(ctx, db, policy)
```

The planner uses:

```text
end_offset       = bucket width
start_offset     = max(2 * bucket width, late-arrival window + bucket width)
schedule_interval = supplied schedule, or bucket width
```

This leaves the open bucket unmaterialized and revisits delayed writes. The
application supplies its own late-arrival or blockchain-reorg tolerance; this
package has no blockchain semantics. Calendar or timezone-aligned buckets need
explicit offsets.

`AddRefreshPolicy`, `InspectRefreshPolicy`, `EnsureRefreshPolicy`,
`ReconcileRefreshPolicy`, `ReplaceRefreshPolicy`, and `RemoveRefreshPolicy`
cover the full lifecycle. `Ensure` reports `ErrPolicyDrift`; `Reconcile`
transactionally removes and re-adds a drifted policy. `IfNotExists` is not
reconciliation. `IncludeTieredData` preserves the optional tiered-data setting,
and batching fields map to stable TimescaleDB 2.20 arguments.
Open-ended start offsets are explicit. An open-ended end offset additionally
requires `AllowIncompleteBucket` because it includes current incomplete data.

Manual refresh uses Timescale's procedure, never PostgreSQL `REFRESH
MATERIALIZED VIEW`:

```go
err := timescaledb.RefreshContinuousAggregate(
    ctx, db, aggregate.Relation(), from, to, true,
)
```

The end is exclusive. Timescale refreshes only complete buckets wholly inside
the range. `PlanRefreshBatches` creates a bounded list of half-open ranges for a
large backfill and errors before exceeding the caller's maximum batch count.
Its start, end, and generated boundaries must be aggregate bucket boundaries;
choose a batch width that is an integer number of buckets. Calendar,
timezone-aligned, custom-origin, and offset boundary calculations remain
application-owned.

## Retention and columnstore

Retention is destructive and therefore uses explicit names:

```go
retention := timescaledb.NewRetentionPolicy(
    timescaledb.FixedInterval(90 * 24 * time.Hour),
)
_, err := timescaledb.EnsureRetentionPolicy(ctx, db, source, retention)
```

The package supports inspect, ensure, reconcile, replace, and remove for both
retention and columnstore policies. Package defaults are deterministic: 24
hours for retention scheduling and 12 hours for columnstore scheduling.

Enable columnstore settings before adding a policy:

```go
settings := timescaledb.NewColumnstoreSettings().
    SegmentBy("series_id").
    OrderBy(timescaledb.ColumnstoreDescending("observed_at"))

if err := timescaledb.EnableHypertableColumnstore(ctx, db, source, settings); err != nil {
    return err
}

policy := timescaledb.NewColumnstorePolicy(
    timescaledb.FixedInterval(30 * 24 * time.Hour),
)
_, err := timescaledb.ReconcileColumnstorePolicy(ctx, db, source, policy)
```

`ValidateLifecycle` returns structured diagnostics without changing the
configuration. It detects columnstore conversion before the expected late
update window, columnstore age at/after raw retention, refresh windows that
reach deleted raw history, and backfills that require already-retained data.

## Transactions, migrations, and metadata

All runtime operations take `context.Context` first and
`*pgxext.DataSource` directly:

```go
if err := hypertable.Apply(ctx, ds); err != nil {
    return err
}
```

Policy replacement and drift reconciliation open and commit their own pgx
transaction through the data source so removal and re-creation stay atomic.
They do not accept a caller-owned `pgx.Tx`.

Builders return migration-safe SQL. Use that string as an existing
`migration.Migration.UpQuery`; the application supplies a rollback appropriate
for its own table and data.

Typed inspection uses documented public views:

- `timescaledb_information.hypertables`
- `timescaledb_information.dimensions`
- `timescaledb_information.chunks`
- `timescaledb_information.continuous_aggregates`
- `timescaledb_information.jobs`
- `timescaledb_information.job_stats`

TimescaleDB 2.20 does not expose a job's timezone in the public `jobs` view.
Policy semantic comparison therefore has one isolated compatibility dependency
on the read-only `timezone` column of `_timescaledb_config.bgw_job`, on which
TimescaleDB grants public `SELECT`. No other internal catalogs are queried.

## Integration tests

The `just` integration recipe runs the complete repository test suite against
separate vanilla PostgreSQL and TimescaleDB containers for each matrix row:

| PostgreSQL | TimescaleDB |
| --- | --- |
| 16 | 2.20.2 |
| 17 | 2.20.2 |
| 18 | 2.23.1 |

```sh
just test-integration
# Equivalent short alias:
just test
```

This requires `just` and Docker. The recipe uses `docker run` directly and
publishes each database on an ephemeral localhost port.

The recipe starts one pair at a time. The root, `functional`, `migration`,
`notification`, and `repository` integration suites use the vanilla PostgreSQL
server, while the `timescaledb` suite uses the matching TimescaleDB server. It
verifies both PostgreSQL majors and the exact TimescaleDB extension version,
treats missing integration-test URLs as failures, runs all unit and integration
tests with atomic coverage, and removes the containers and anonymous volumes
even when a test fails. No Compose file is needed.

Coverage profiles are retained as:

```text
.coverage/postgres-16-timescaledb-2.20.2.coverprofile
.coverage/postgres-17-timescaledb-2.20.2.coverprofile
.coverage/postgres-18-timescaledb-2.23.1.coverprofile
```

Set `PGXEXT_COVERAGE_DIR` to choose another output directory. The TimescaleDB
suite exercises extension and capability inspection, both hypertable creation
paths, frame and gapfill modes, hierarchical continuous aggregates, real-time
mode, manual refresh, every policy lifecycle, public metadata and job views,
and columnstore enable/disable behavior. It does not wait for background jobs;
job registration is inspected and deterministic aggregate assertions use
manual refreshes. The generic `functional` package also applies, replaces,
refreshes, and drops its database objects on every PostgreSQL matrix row.

Set `PGXEXT_RACE=1` to run the same complete matrix with Go's race detector:

```sh
PGXEXT_RACE=1 just test-integration
```

## Non-goals

The package does not:

- define domain tables, entity-per-table layouts, or application repositories;
- implement prices, pools, ticks, candles, distributions, or DEX logic;
- replace ordinary pgx inserts and updates;
- install the extension implicitly or own `DataSource` lifecycle;
- start Go schedulers or hidden goroutines;
- accept unchecked identifiers or runtime expressions;
- make retention decisions for an application;
- claim integer time-dimension support;
- make hypertable conversion automatically reversible.

Official references: [hypertable CREATE
TABLE](https://docs.timescale.com/api/latest/hypertable/create_table/),
[conversion](https://docs.timescale.com/api/latest/hypertable/create_hypertable/),
[time_bucket](https://docs.timescale.com/api/latest/hyperfunctions/time_bucket/),
[gapfill](https://docs.timescale.com/api/latest/hyperfunctions/gapfilling/time_bucket_gapfill/),
[continuous aggregates](https://docs.timescale.com/api/latest/continuous-aggregates/create_materialized_view/),
[real-time aggregates](https://docs.timescale.com/use-timescale/latest/continuous-aggregates/real-time-aggregates/),
[refresh policies](https://docs.timescale.com/api/latest/continuous-aggregates/add_continuous_aggregate_policy/),
[manual refresh](https://docs.timescale.com/api/latest/continuous-aggregates/refresh_continuous_aggregate/),
[retention](https://docs.timescale.com/api/latest/data-retention/add_retention_policy/),
[columnstore policies](https://docs.timescale.com/api/latest/hypercore/add_columnstore_policy/),
and [information views](https://docs.timescale.com/api/latest/informational-views/).
