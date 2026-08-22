package notification

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/0x626f/pgxext"
	"github.com/0x626f/pgxext/repository"
)

type repositoryNotificationItem struct {
	ID     int    `db:"id"`
	Name   string `db:"name"`
	Status string `db:"status"`
}

func integrationDS(t *testing.T) *pgxext.DataSource {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		if os.Getenv("PGXEXT_REQUIRE_INTEGRATION") == "1" {
			t.Fatal("TEST_DATABASE_URL not set while integration tests are required")
		}
		t.Skip("TEST_DATABASE_URL not set; skipping integration test")
	}
	cfg := pgxext.NewConfig()
	if _, err := cfg.WithURL(url); err != nil {
		t.Fatalf("config: %v", err)
	}
	ds, err := pgxext.NewDataSource(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewDataSource: %v", err)
	}
	t.Cleanup(ds.Close)
	return ds
}

func setupNotificationsDB(t *testing.T, ds *pgxext.DataSource) {
	t.Helper()
	ctx := context.Background()
	for _, stmt := range []string{
		`DROP TABLE IF EXISTS test_notifications`,
		`CREATE TABLE test_notifications (
			id SERIAL PRIMARY KEY,
			name TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT ''
		)`,
	} {
		if _, err := ds.Exec(ctx, stmt); err != nil {
			t.Fatalf("setupNotificationsDB: %v", err)
		}
	}
	t.Cleanup(func() {
		ds.Exec(context.Background(), `DROP TABLE IF EXISTS test_notifications`) //nolint:errcheck
	})
}

func TestNotification_E2E_InsertPayload(t *testing.T) {
	ds := integrationDS(t)
	setupNotificationsDB(t, ds)

	const channel = "test.notifications.insert"
	notifier := NewNotification("test_notifications", channel).
		On(Insert).
		WithPayloadProperties(ToState, TableName, RowID, CreatedAt)
	if err := notifier.Apply(context.Background(), ds); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	t.Cleanup(func() {
		if sql, err := notifier.DropSQL(); err == nil {
			ds.Exec(context.Background(), sql) //nolint:errcheck
		}
	})

	event := waitForNotification(t, ds, channel, func() {
		ds.Exec(context.Background(), `INSERT INTO test_notifications (name, status) VALUES ($1, $2)`, "alpha", "new") //nolint:errcheck
	})

	if event.Channel != channel {
		t.Fatalf("Channel = %q, want %q", event.Channel, channel)
	}
	if event.Payload.TableName != "test_notifications" {
		t.Fatalf("tableName = %q, want test_notifications", event.Payload.TableName)
	}
	if event.Payload.RowID == nil {
		t.Fatal("rowId is nil")
	}
	if event.Payload.CreatedAt == "" {
		t.Fatal("createdAt is empty")
	}

	var toState struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(event.Payload.ToState, &toState); err != nil {
		t.Fatalf("unmarshal toState: %v", err)
	}
	if toState.Name != "alpha" || toState.Status != "new" {
		t.Fatalf("toState = %+v, want alpha/new", toState)
	}
	if string(event.Payload.FromState) != "null" && len(event.Payload.FromState) != 0 {
		t.Fatalf("fromState = %s, want omitted or null", event.Payload.FromState)
	}
}

func TestNotification_E2E_UpdatePayloadIncludesOldAndNew(t *testing.T) {
	ds := integrationDS(t)
	setupNotificationsDB(t, ds)

	if _, err := ds.Exec(context.Background(), `INSERT INTO test_notifications (name, status) VALUES ($1, $2)`, "alpha", "old"); err != nil {
		t.Fatalf("insert seed: %v", err)
	}

	const channel = "test.notifications.update"
	notifier := NewNotification("test_notifications", channel).
		On(Update).
		WithPayloadProperties(FromState, ToState, TableName, RowID, CreatedAt)
	if err := notifier.Apply(context.Background(), ds); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	t.Cleanup(func() {
		if sql, err := notifier.DropSQL(); err == nil {
			ds.Exec(context.Background(), sql) //nolint:errcheck
		}
	})

	event := waitForNotification(t, ds, channel, func() {
		ds.Exec(context.Background(), `UPDATE test_notifications SET status = $1 WHERE name = $2`, "new", "alpha") //nolint:errcheck
	})

	var fromState, toState struct {
		Name   string `json:"name"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(event.Payload.FromState, &fromState); err != nil {
		t.Fatalf("unmarshal fromState: %v", err)
	}
	if err := json.Unmarshal(event.Payload.ToState, &toState); err != nil {
		t.Fatalf("unmarshal toState: %v", err)
	}
	if fromState.Status != "old" || toState.Status != "new" {
		t.Fatalf("status transition = %q -> %q, want old -> new", fromState.Status, toState.Status)
	}
}

func TestNotification_E2E_EmptyPayload(t *testing.T) {
	ds := integrationDS(t)
	setupNotificationsDB(t, ds)

	const channel = "test.notifications.empty"
	notifier := NewNotification("test_notifications", channel).On(Insert).EmptyPayload()
	if err := notifier.Apply(context.Background(), ds); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	t.Cleanup(func() {
		if sql, err := notifier.DropSQL(); err == nil {
			ds.Exec(context.Background(), sql) //nolint:errcheck
		}
	})

	event := waitForNotification(t, ds, channel, func() {
		ds.Exec(context.Background(), `INSERT INTO test_notifications (name) VALUES ($1)`, "empty") //nolint:errcheck
	})
	if event.Raw != "{}" {
		t.Fatalf("Raw = %q, want {}", event.Raw)
	}
	if event.Payload.TableName != "" || event.Payload.RowID != nil || event.Payload.CreatedAt != "" ||
		len(event.Payload.FromState) != 0 || len(event.Payload.ToState) != 0 {
		t.Fatalf("Payload = %+v, want empty", event.Payload)
	}
}

func TestNotification_E2E_RepositoryInsertPayload(t *testing.T) {
	ds := integrationDS(t)
	setupNotificationsDB(t, ds)

	const channel = "test.notifications.repository.insert"
	notifier := NewNotification("test_notifications", channel).
		On(Insert).
		WithPayloadProperties(ToState, TableName, RowID, CreatedAt)
	if err := notifier.Apply(context.Background(), ds); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	t.Cleanup(func() {
		if sql, err := notifier.DropSQL(); err == nil {
			ds.Exec(context.Background(), sql) //nolint:errcheck
		}
	})

	repo := repository.NewRepository[repositoryNotificationItem](ds, "test_notifications")
	event := waitForNotification(t, ds, channel, func() {
		if _, err := repo.Insert().
			Set("name", "repo-alpha").
			Set("status", "created").
			Execute(context.Background()); err != nil {
			t.Errorf("repository insert: %v", err)
		}
	})

	if event.Payload.TableName != "test_notifications" {
		t.Fatalf("tableName = %q, want test_notifications", event.Payload.TableName)
	}
	if event.Payload.RowID == nil {
		t.Fatal("rowId is nil")
	}

	var toState repositoryNotificationItem
	if err := json.Unmarshal(event.Payload.ToState, &toState); err != nil {
		t.Fatalf("unmarshal toState: %v", err)
	}
	if toState.Name != "repo-alpha" || toState.Status != "created" {
		t.Fatalf("toState = %+v, want repo-alpha/created", toState)
	}
}

func waitForNotification(t *testing.T, ds *pgxext.DataSource, channel string, emit func()) Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	events := make(chan Event, 1)
	done := make(chan error, 1)
	ready := make(chan struct{})
	stop := errors.New("stop notification consumer")
	go func() {
		done <- NewConsumer(ds, channel).listen(ctx, ready, func(_ context.Context, event Event) error {
			events <- event
			return stop
		})
	}()

	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("consumer before LISTEN: %v", err)
	case <-ctx.Done():
		t.Fatalf("timed out waiting for LISTEN on %q", channel)
	}

	emit()

	for {
		select {
		case event := <-events:
			err := <-done
			if err != nil && !errors.Is(err, stop) {
				t.Fatalf("consumer: %v", err)
			}
			return event
		case err := <-done:
			if err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("consumer: %v", err)
			}
		case <-ctx.Done():
			t.Fatalf("timed out waiting for notification on %q", channel)
		}
	}
}
