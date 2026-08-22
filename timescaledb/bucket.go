package timescaledb

import (
	"fmt"
	"strings"
	"time"
)

type bucketConfig struct {
	width     Interval
	timezone  string
	origin    *time.Time
	offset    Interval
	offsetSet bool
}

func (b bucketConfig) validate(gapfill bool) error {
	if err := b.width.Validate(); err != nil {
		return fmt.Errorf("timescaledb: invalid bucket width: %w", err)
	}
	if b.timezone != "" {
		if _, err := time.LoadLocation(b.timezone); err != nil {
			return fmt.Errorf("timescaledb: invalid timezone %q: %w", b.timezone, err)
		}
	}
	if b.offsetSet {
		if err := b.offset.validateOffset(); err != nil {
			return err
		}
	}
	if gapfill && (b.origin != nil || b.offsetSet) {
		return fmt.Errorf("timescaledb: time_bucket_gapfill supports timezone but not origin or offset")
	}
	if b.timezone == "" && b.origin != nil && b.offsetSet {
		return fmt.Errorf("timescaledb: origin and offset together require the timezone overload")
	}
	return nil
}

func (b bucketConfig) parameterizedSQL(timeColumn string, gapfill bool, args *[]any) (string, error) {
	if err := b.validate(gapfill); err != nil {
		return "", err
	}
	width, err := b.width.pgValue()
	if err != nil {
		return "", err
	}
	*args = append(*args, width)
	parts := []string{fmt.Sprintf("$%d::interval", len(*args)), timeColumn}
	if b.timezone != "" {
		*args = append(*args, b.timezone)
		parts = append(parts, fmt.Sprintf("timezone => $%d::text", len(*args)))
	}
	if !gapfill && b.origin != nil {
		*args = append(*args, b.origin.UTC())
		parts = append(parts, fmt.Sprintf("origin => $%d::timestamptz", len(*args)))
	}
	if !gapfill && b.offsetSet {
		offset, err := b.offset.offsetPGValue()
		if err != nil {
			return "", err
		}
		*args = append(*args, offset)
		parts = append(parts, fmt.Sprintf(`"offset" => $%d::interval`, len(*args)))
	}
	function := "time_bucket"
	if gapfill {
		function = "time_bucket_gapfill"
	}
	return function + "(" + strings.Join(parts, ", ") + ")", nil
}

func (b bucketConfig) literalSQL(timeColumn string) (string, error) {
	if err := b.validate(false); err != nil {
		return "", err
	}
	width, err := b.width.SQL()
	if err != nil {
		return "", err
	}
	parts := []string{width, timeColumn}
	if b.timezone != "" {
		parts = append(parts, "timezone => "+sqlLiteral(b.timezone))
	}
	if b.origin != nil {
		parts = append(parts, "origin => TIMESTAMPTZ "+sqlLiteral(b.origin.UTC().Format(time.RFC3339Nano)))
	}
	if b.offsetSet {
		offset, err := b.offset.offsetSQL()
		if err != nil {
			return "", err
		}
		parts = append(parts, `"offset" => `+offset)
	}
	return "time_bucket(" + strings.Join(parts, ", ") + ")", nil
}
