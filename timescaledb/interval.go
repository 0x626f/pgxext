package timescaledb

import (
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
)

type intervalKind uint8

const (
	intervalNull intervalKind = iota
	intervalFixed
	intervalMonths
)

// Interval is a structural PostgreSQL interval. The zero value represents SQL
// NULL where an API explicitly permits an open-ended interval. Widths and
// schedules reject it. Fixed values have PostgreSQL's microsecond resolution.
// Calendar values contain whole months only and cannot be mixed with fixed
// components, matching time_bucket's month-width restriction.
type Interval struct {
	kind     intervalKind
	duration time.Duration
	months   int32
}

// FixedInterval constructs a fixed-duration interval. Validation occurs when
// the interval is used or when Validate is called.
func FixedInterval(duration time.Duration) Interval {
	return Interval{kind: intervalFixed, duration: duration}
}

// CalendarMonths constructs a calendar interval containing whole months.
// Month widths are intentionally kept separate from fixed components.
func CalendarMonths(months int32) Interval {
	return Interval{kind: intervalMonths, months: months}
}

// OpenEndedInterval returns the SQL NULL interval used for an open-ended
// refresh offset. It is invalid as a bucket width, chunk interval, or schedule.
func OpenEndedInterval() Interval {
	return Interval{}
}

// IsOpenEnded reports whether this interval represents SQL NULL.
func (i Interval) IsOpenEnded() bool {
	return i.kind == intervalNull
}

// IsCalendar reports whether this is a whole-month calendar interval.
func (i Interval) IsCalendar() bool {
	return i.kind == intervalMonths
}

// Duration returns the fixed duration and true, or zero and false for calendar
// and open-ended intervals.
func (i Interval) Duration() (time.Duration, bool) {
	if i.kind != intervalFixed {
		return 0, false
	}
	return i.duration, true
}

// Months returns the whole-month count and true for calendar intervals.
func (i Interval) Months() (int32, bool) {
	if i.kind != intervalMonths {
		return 0, false
	}
	return i.months, true
}

// Validate checks that an interval is a positive supported width.
func (i Interval) Validate() error {
	switch i.kind {
	case intervalFixed:
		if i.duration <= 0 {
			return fmt.Errorf("timescaledb: fixed interval must be positive")
		}
		if i.duration%time.Microsecond != 0 {
			return fmt.Errorf("timescaledb: fixed interval must use PostgreSQL microsecond precision")
		}
		return nil
	case intervalMonths:
		if i.months <= 0 {
			return fmt.Errorf("timescaledb: calendar month interval must be positive")
		}
		return nil
	case intervalNull:
		return fmt.Errorf("timescaledb: interval is open-ended")
	default:
		return fmt.Errorf("timescaledb: unsupported interval representation")
	}
}

func (i Interval) validateOffset() error {
	if i.kind != intervalFixed {
		return fmt.Errorf("timescaledb: bucket offset must be a fixed interval")
	}
	if i.duration == 0 {
		return fmt.Errorf("timescaledb: bucket offset must not be zero")
	}
	if i.duration%time.Microsecond != 0 {
		return fmt.Errorf("timescaledb: bucket offset must use PostgreSQL microsecond precision")
	}
	return nil
}

// SQL renders a validated positive interval for application-owned DDL.
func (i Interval) SQL() (string, error) {
	if err := i.Validate(); err != nil {
		return "", err
	}
	return i.sql(false), nil
}

func (i Interval) offsetSQL() (string, error) {
	if err := i.validateOffset(); err != nil {
		return "", err
	}
	return i.sql(true), nil
}

func (i Interval) sql(_ bool) string {
	switch i.kind {
	case intervalFixed:
		return fmt.Sprintf("INTERVAL '%d microseconds'", i.duration/time.Microsecond)
	case intervalMonths:
		return fmt.Sprintf("INTERVAL '%d months'", i.months)
	default:
		return "NULL"
	}
}

func (i Interval) optionText() (string, error) {
	if err := i.Validate(); err != nil {
		return "", err
	}
	if i.kind == intervalMonths {
		return fmt.Sprintf("%d months", i.months), nil
	}
	return fmt.Sprintf("%d microseconds", i.duration/time.Microsecond), nil
}

func (i Interval) pgValue() (any, error) {
	if err := i.Validate(); err != nil {
		return nil, err
	}
	return i.pgValueUnchecked(), nil
}

func (i Interval) offsetPGValue() (any, error) {
	if err := i.validateOffset(); err != nil {
		return nil, err
	}
	return i.pgValueUnchecked(), nil
}

func (i Interval) nullablePGValue() (any, error) {
	if i.IsOpenEnded() {
		return nil, nil
	}
	return i.pgValue()
}

func (i Interval) pgValueUnchecked() pgtype.Interval {
	value := pgtype.Interval{Valid: true}
	if i.kind == intervalMonths {
		value.Months = i.months
	} else {
		value.Microseconds = i.duration.Microseconds()
	}
	return value
}

func intervalFromPG(value pgtype.Interval) (Interval, error) {
	if !value.Valid {
		return OpenEndedInterval(), nil
	}
	if value.Months != 0 && (value.Days != 0 || value.Microseconds != 0) {
		return Interval{}, fmt.Errorf("timescaledb: database returned mixed month and fixed interval components")
	}
	if value.Months != 0 {
		return CalendarMonths(value.Months), nil
	}
	if value.Days > math.MaxInt64/(24*60*60*1_000_000) || value.Days < math.MinInt64/(24*60*60*1_000_000) {
		return Interval{}, fmt.Errorf("timescaledb: database interval overflows fixed duration")
	}
	dayMicros := int64(value.Days) * 24 * 60 * 60 * 1_000_000
	if (dayMicros > 0 && value.Microseconds > math.MaxInt64-dayMicros) ||
		(dayMicros < 0 && value.Microseconds < math.MinInt64-dayMicros) {
		return Interval{}, fmt.Errorf("timescaledb: database interval overflows fixed duration")
	}
	micros := dayMicros + value.Microseconds
	if micros > math.MaxInt64/int64(time.Microsecond) || micros < math.MinInt64/int64(time.Microsecond) {
		return Interval{}, fmt.Errorf("timescaledb: database interval overflows time.Duration")
	}
	return Interval{kind: intervalFixed, duration: time.Duration(micros) * time.Microsecond}, nil
}

func compareIntervals(left, right Interval) (int, error) {
	if left.IsOpenEnded() || right.IsOpenEnded() {
		return 0, fmt.Errorf("timescaledb: cannot order open-ended intervals")
	}
	if left.kind != right.kind {
		return 0, fmt.Errorf("timescaledb: cannot compare fixed and calendar intervals")
	}
	switch left.kind {
	case intervalFixed:
		if left.duration < right.duration {
			return -1, nil
		}
		if left.duration > right.duration {
			return 1, nil
		}
	case intervalMonths:
		if left.months < right.months {
			return -1, nil
		}
		if left.months > right.months {
			return 1, nil
		}
	default:
		return 0, fmt.Errorf("timescaledb: unsupported interval representation")
	}
	return 0, nil
}

func intervalsEqual(left, right Interval) bool {
	if left.kind != right.kind {
		return false
	}
	return left.duration == right.duration && left.months == right.months
}
