package notification

import (
	"context"
	"fmt"
	"strings"

	"github.com/0x626f/pgxext"
)

// Notification creates PostgreSQL trigger SQL that emits JSON NOTIFY payloads.
type Notification struct {
	table      string
	eventName  string
	operations []Operation
	rowID      string
	props      []PayloadProperty
}

// NewNotification returns a trigger notification builder for table and eventName.
// eventName is used as the PostgreSQL NOTIFY channel.
func NewNotification(table, eventName string) *Notification {
	return &Notification{
		table:      table,
		eventName:  eventName,
		operations: []Operation{Insert, Update, Delete},
		rowID:      "id",
	}
}

// On restricts the trigger to the listed operations. If omitted, all supported
// row operations are used.
func (b *Notification) On(operations ...Operation) *Notification {
	b.operations = append([]Operation(nil), operations...)
	return b
}

// WithRowIDColumn sets the column used for the rowId payload property.
func (b *Notification) WithRowIDColumn(column string) *Notification {
	b.rowID = column
	return b
}

// WithPayloadProperties sets the exact top-level JSON properties to emit.
// Passing no properties intentionally creates an empty JSON object payload.
func (b *Notification) WithPayloadProperties(props ...PayloadProperty) *Notification {
	b.props = append([]PayloadProperty(nil), props...)
	return b
}

// EmptyPayload configures the trigger to emit "{}".
func (b *Notification) EmptyPayload() *Notification {
	b.props = nil
	return b
}

// BuildSQL returns SQL that creates or replaces the notification trigger
// function and trigger.
func (b *Notification) BuildSQL() (string, error) {
	qualifiedTable, err := quoteQualifiedIdentifier(b.table)
	if err != nil {
		return "", err
	}
	if _, err := quoteIdentifier(b.eventName); err != nil {
		return "", err
	}
	if err := validateIdentifier(b.rowID); err != nil {
		return "", err
	}
	if len(b.operations) == 0 {
		return "", fmt.Errorf("notification: at least one operation is required")
	}

	fnName := triggerFunctionName(b.table, b.eventName)
	triggerName := triggerName(b.table, b.eventName)
	quotedFn, err := quoteIdentifier(fnName)
	if err != nil {
		return "", err
	}
	quotedTrigger, err := quoteIdentifier(triggerName)
	if err != nil {
		return "", err
	}
	ops, err := operationsSQL(b.operations)
	if err != nil {
		return "", err
	}
	payloadSQL, err := b.payloadSQL()
	if err != nil {
		return "", err
	}

	return fmt.Sprintf(`CREATE OR REPLACE FUNCTION %s()
RETURNS trigger AS $$
DECLARE
	payload jsonb;
BEGIN
	payload := %s;
	PERFORM pg_notify(%s, payload::text);
	RETURN COALESCE(NEW, OLD);
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS %s ON %s;
CREATE TRIGGER %s
AFTER %s ON %s
FOR EACH ROW EXECUTE FUNCTION %s();`,
		quotedFn,
		payloadSQL,
		sqlLiteral(b.eventName),
		quotedTrigger,
		qualifiedTable,
		quotedTrigger,
		ops,
		qualifiedTable,
		quotedFn,
	), nil
}

// DropSQL returns SQL that drops the trigger and function created by BuildSQL.
func (b *Notification) DropSQL() (string, error) {
	qualifiedTable, err := quoteQualifiedIdentifier(b.table)
	if err != nil {
		return "", err
	}
	fnName := triggerFunctionName(b.table, b.eventName)
	triggerName := triggerName(b.table, b.eventName)
	quotedFn, err := quoteIdentifier(fnName)
	if err != nil {
		return "", err
	}
	quotedTrigger, err := quoteIdentifier(triggerName)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON %s;\nDROP FUNCTION IF EXISTS %s();", quotedTrigger, qualifiedTable, quotedFn), nil
}

// Apply executes BuildSQL against ds.
func (b *Notification) Apply(ctx context.Context, ds *pgxext.DataSource) error {
	sql, err := b.BuildSQL()
	if err != nil {
		return err
	}
	_, err = ds.Exec(ctx, sql)
	return err
}

func (b *Notification) payloadSQL() (string, error) {
	if len(b.props) == 0 {
		return "'{}'::jsonb", nil
	}
	parts := make([]string, 0, len(b.props)*2)
	seen := make(map[PayloadProperty]struct{}, len(b.props))
	for _, prop := range b.props {
		if _, ok := seen[prop]; ok {
			continue
		}
		seen[prop] = struct{}{}
		value, err := b.payloadValue(prop)
		if err != nil {
			return "", err
		}
		parts = append(parts, sqlLiteral(string(prop)), value)
	}
	return "jsonb_build_object(" + strings.Join(parts, ", ") + ")", nil
}

func (b *Notification) payloadValue(prop PayloadProperty) (string, error) {
	switch prop {
	case FromState:
		return "CASE WHEN TG_OP IN ('UPDATE', 'DELETE') THEN to_jsonb(OLD) ELSE NULL END", nil
	case ToState:
		return "CASE WHEN TG_OP IN ('INSERT', 'UPDATE') THEN to_jsonb(NEW) ELSE NULL END", nil
	case TableName:
		return "TG_TABLE_NAME", nil
	case RowID:
		if err := validateIdentifier(b.rowID); err != nil {
			return "", err
		}
		return fmt.Sprintf("CASE WHEN TG_OP = 'DELETE' THEN to_jsonb(OLD)->%s ELSE to_jsonb(NEW)->%s END", sqlLiteral(b.rowID), sqlLiteral(b.rowID)), nil
	case CreatedAt:
		return "to_jsonb(clock_timestamp())", nil
	default:
		return "", fmt.Errorf("notification: unknown payload property %q", prop)
	}
}

func operationsSQL(operations []Operation) (string, error) {
	parts := make([]string, len(operations))
	for i, op := range operations {
		switch op {
		case Insert, Update, Delete:
			parts[i] = string(op)
		default:
			return "", fmt.Errorf("notification: unsupported operation %q", op)
		}
	}
	return strings.Join(parts, " OR "), nil
}

func triggerFunctionName(table, eventName string) string {
	return "pgxext_notify_" + sanitizeName(table) + "_" + sanitizeName(eventName)
}

func triggerName(table, eventName string) string {
	return "pgxext_notify_" + sanitizeName(table) + "_" + sanitizeName(eventName) + "_trg"
}

func sanitizeName(value string) string {
	value = strings.ReplaceAll(value, ".", "_")
	var sb strings.Builder
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' {
			sb.WriteRune(r)
		}
	}
	if sb.Len() == 0 {
		return "event"
	}
	return sb.String()
}
