package notification

import (
	"encoding/json"
	"testing"
)

func TestPayload_Unmarshal(t *testing.T) {
	raw := []byte(`{
		"fromState": {"id": 1, "name": "old"},
		"toState": {"id": 1, "name": "new"},
		"tableName": "users",
		"rowId": 1,
		"createdAt": "2026-05-25T12:00:00Z"
	}`)
	var payload Payload
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if payload.TableName != "users" {
		t.Fatalf("TableName = %q, want users", payload.TableName)
	}
	if payload.CreatedAt != "2026-05-25T12:00:00Z" {
		t.Fatalf("CreatedAt = %q", payload.CreatedAt)
	}
	if string(payload.FromState) == "" || string(payload.ToState) == "" {
		t.Fatal("state payloads were not preserved")
	}
}

func TestQuoteIdentifier_AllowsCustomChannelName(t *testing.T) {
	got, err := quoteIdentifier("user.changed")
	if err != nil {
		t.Fatalf("quoteIdentifier: %v", err)
	}
	if got != `"user.changed"` {
		t.Fatalf("got %q, want %q", got, `"user.changed"`)
	}
}
