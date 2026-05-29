<div align="center">
    <pre style="background: none;">
 ███████████    █████████  █████ █████    ██████████ █████ █████ ███████████
░░███░░░░░███  ███░░░░░███░░███ ░░███    ░░███░░░░░█░░███ ░░███ ░█░░░███░░░█
 ░███    ░███ ███     ░░░  ░░███ ███      ░███  █ ░  ░░███ ███  ░   ░███  ░
 ░██████████ ░███           ░░█████       ░██████     ░░█████       ░███
 ░███░░░░░░  ░███    █████   ███░███      ░███░░█      ███░███      ░███
 ░███        ░░███  ░░███   ███ ░░███     ░███ ░   █  ███ ░░███     ░███
 █████        ░░█████████  █████ █████    ██████████ █████ █████    █████
░░░░░          ░░░░░░░░░  ░░░░░ ░░░░░    ░░░░░░░░░░ ░░░░░ ░░░░░    ░░░░░
    </pre>
</div>

<div align="center">
    <h3>A thin pgx/v5 connection-pool wrapper with migrations and a generic query builder.</h3>
</div>

---

## Overview

`pgxext` wraps `pgxpool` to provide:

- **Config** — fluent builder for connection settings, TLS, pool parameters, and runtime params.
- **DataSource** — a `*pgxpool.Pool` wrapper exposing `Query`, `Exec`, `QueryRow`, transactions, and batch operations.
- **Migration** — transactional SQL migration runner backed by a `migrations` table.
- **Repository** — generic, type-safe query builder (SELECT / INSERT / UPDATE / DELETE) with JOIN and COUNT support.
- **Notification** — trigger builder and LISTEN consumer for JSON PostgreSQL notifications.
- **Database** — builders for PostgreSQL views, materialized views, and functions.

---

## Quick start

### Config & DataSource

```go
ds, err := pgxext.NewDataSource(ctx,
    pgxext.NewConfig().
        WithHost("localhost").
        WithPort(5432).
        WithDatabase("mydb").
        WithUser("alice").
        WithPassword("secret").
        WithMaxConns(10),
)
```

Or from a URL:

```go
cfg, err := pgxext.NewConfig().WithURL("postgres://alice:secret@localhost/mydb?pool_max_conns=10")
ds, err := pgxext.NewDataSource(ctx, cfg)
```

### Migrations

```go
var schema = migration.MigrationSet{
    {
        Name:      "001_create_users",
        UpQuery:   `CREATE TABLE users (id SERIAL PRIMARY KEY, name TEXT NOT NULL)`,
        DownQuery: `DROP TABLE users`,
    },
}

m := migration.NewMigrator(ctx, ds)
m.Up(schema)   // apply
m.Down(schema) // revert
```

### Repository

Define a model with `db` struct tags (pgx `RowToAddrOfStructByName` convention):

```go
type User struct {
    ID    int    `db:"id"`
    Name  string `db:"name"`
    Email string `db:"email"`
}
```

Create a repository once and reuse it:

```go
repo := repository.NewRepository[User](ds, "users")
```

**Select**

```go
users, err := repo.Select().
    Where("name", repository.Like, "%alice%").
    OrderBy("name", repository.ASC).
    Limit(20).
    Execute(ctx)
```

**Count**

```go
n, err := repo.Select().
    Where("email", repository.Like, "%@example.com").
    Count(ctx)
```

**Insert**

```go
n, err := repo.Insert().
    Set("name", "Alice").
    Set("email", "alice@example.com").
    Execute(ctx)
```

**Update**

```go
n, err := repo.Update().
    Set("email", "new@example.com").
    Where("id", repository.Equals, 42).
    Execute(ctx)
```

**Delete**

```go
n, err := repo.Delete().
    Where("id", repository.Equals, 42).
    Execute(ctx)
```

**Join**

```go
type UserOrder struct {
    UserID  int    `db:"id"`
    Name    string `db:"name"`
    OrderID int    `db:"order_id"`
}

repo := repository.NewRepository[UserOrder](ds, "users")
rows, err := repo.Select().
    Join("orders", "users.id", repository.Equals, "orders.user_id").
    Where("orders.status", repository.Equals, "paid").
    Execute(ctx)
```

### Notifications

Create a trigger that sends JSON payloads on selected row operations:

```go
notificationSQL := notification.NewNotification("users", "user.changed").
    On(notification.Insert, notification.Update, notification.Delete).
    WithPayloadProperties(
        notification.FromState,
        notification.ToState,
        notification.TableName,
        notification.RowID,
        notification.CreatedAt,
    )

err := notificationSQL.Apply(ctx, ds)
```

The generated payload uses these top-level properties:

```json
{
  "fromState": {},
  "toState": {},
  "tableName": "users",
  "rowId": 42,
  "createdAt": "2026-05-25T12:00:00Z"
}
```

Use `EmptyPayload()` or omit `WithPayloadProperties` to emit `{}`.

Consume notifications with a dedicated LISTEN connection:

```go
consumer := notification.NewConsumer(ds, "user.changed")
err := consumer.Listen(ctx, func(ctx context.Context, event notification.Event) error {
    fmt.Println(event.Payload.TableName, event.Payload.RowID)
    return nil
})
```

### Database objects

Build and apply database-level objects directly or embed the generated SQL in migrations:

```go
view := database.NewView("public.active_users").
    OrReplace().
    As(`SELECT id, email FROM users WHERE active`)

err := view.Apply(ctx, ds)
```

```go
matView := database.NewMaterializedView("public.user_totals").
    IfNotExists().
    As(`SELECT user_id, count(*) AS total FROM orders GROUP BY user_id`).
    WithNoData()

err := matView.Apply(ctx, ds)
err = matView.Refresh(ctx, ds, true, true)
```

```go
fn := database.NewFunction("public.normalize_email").
    OrReplace().
    WithArguments("value text").
    Returns("text").
    Language("sql").
    WithVolatility(database.Immutable).
    Strict().
    Body(`SELECT lower(trim(value))`)

err := fn.Apply(ctx, ds)
```

### Utilities

```go
// Scan one row into *T (nil on no rows)
user, err := pgxext.CollectOneRow[User](rows)

// Scan all rows into []*T
users, err := pgxext.CollectRows[User](rows)

// Inspect a Postgres error code / constraint
if pgErr, ok := pgxext.IsPostgresError(err); ok {
    fmt.Println(pgErr.Code, pgErr.ConstraintName)
}
```
