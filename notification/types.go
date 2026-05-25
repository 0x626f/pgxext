// Package notification provides helpers for PostgreSQL LISTEN/NOTIFY
// consumers and trigger-based notification SQL generation.
package notification

import "encoding/json"

// Operation is a PostgreSQL trigger operation that can emit a notification.
type Operation string

const (
	Insert Operation = "INSERT"
	Update Operation = "UPDATE"
	Delete Operation = "DELETE"
)

// PayloadProperty is a top-level JSON payload property emitted by generated
// notification triggers.
type PayloadProperty string

const (
	FromState PayloadProperty = "fromState"
	ToState   PayloadProperty = "toState"
	TableName PayloadProperty = "tableName"
	RowID     PayloadProperty = "rowId"
	CreatedAt PayloadProperty = "createdAt"
)

// Payload is the conventional JSON shape produced by Notification.
type Payload struct {
	FromState json.RawMessage `json:"fromState,omitempty"`
	ToState   json.RawMessage `json:"toState,omitempty"`
	TableName string          `json:"tableName,omitempty"`
	RowID     any             `json:"rowId,omitempty"`
	CreatedAt string          `json:"createdAt,omitempty"`
}

// Event is a received PostgreSQL notification with an optionally decoded
// payload.
type Event struct {
	PID     uint32
	Channel string
	Raw     string
	Payload Payload
}
