// Package notification provides PostgreSQL LISTEN/NOTIFY helpers.
package notification

import "encoding/json"

// Operation is a PostgreSQL row operation.
type Operation string

const (
	Insert Operation = "INSERT"
	Update Operation = "UPDATE"
	Delete Operation = "DELETE"
)

// PayloadProperty is a JSON payload field.
type PayloadProperty string

const (
	FromState PayloadProperty = "fromState"
	ToState   PayloadProperty = "toState"
	TableName PayloadProperty = "tableName"
	RowID     PayloadProperty = "rowId"
	CreatedAt PayloadProperty = "createdAt"
)

// Payload is a decoded notification payload.
type Payload struct {
	FromState json.RawMessage `json:"fromState,omitempty"`
	ToState   json.RawMessage `json:"toState,omitempty"`
	TableName string          `json:"tableName,omitempty"`
	RowID     any             `json:"rowId,omitempty"`
	CreatedAt string          `json:"createdAt,omitempty"`
}

// Event is a received PostgreSQL notification.
type Event struct {
	PID     uint32
	Channel string
	Raw     string
	Payload Payload
}
