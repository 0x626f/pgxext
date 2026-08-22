package timescaledb

import (
	"fmt"
	"strings"
)

type aggregateKind uint8

const (
	aggregateCount aggregateKind = iota + 1
	aggregateSum
	aggregateAvg
	aggregateMin
	aggregateMax
	aggregateFirst
	aggregateLast
	aggregateTrusted
)

type fillMode uint8

const (
	fillNull fillMode = iota
	fillLOCF
	fillInterpolate
)

// Measure is an aliased aggregate expression. Its fields are private so SQL
// can only be produced by safe constructors or TrustedMeasure.
type Measure struct {
	kind               aggregateKind
	columns            []Column
	trusted            TrustedExpression
	alias              Alias
	fill               fillMode
	treatNullAsMissing bool
	previous           TrustedExpression
	next               TrustedExpression
}

// Count returns COUNT(*) when called without a column and COUNT(column) when
// called with exactly one column.
func Count(columns ...Column) Measure {
	return Measure{kind: aggregateCount, columns: append([]Column(nil), columns...)}
}

// Sum returns SUM(column).
func Sum(column Column) Measure {
	return Measure{kind: aggregateSum, columns: []Column{column}}
}

// Avg returns AVG(column).
func Avg(column Column) Measure {
	return Measure{kind: aggregateAvg, columns: []Column{column}}
}

// Min returns MIN(column).
func Min(column Column) Measure {
	return Measure{kind: aggregateMin, columns: []Column{column}}
}

// Max returns MAX(column).
func Max(column Column) Measure {
	return Measure{kind: aggregateMax, columns: []Column{column}}
}

// First returns TimescaleDB first(value, time).
func First(value, timeColumn Column) Measure {
	return Measure{kind: aggregateFirst, columns: []Column{value, timeColumn}}
}

// Last returns TimescaleDB last(value, time).
func Last(value, timeColumn Column) Measure {
	return Measure{kind: aggregateLast, columns: []Column{value, timeColumn}}
}

// TrustedMeasure wraps an advanced aggregate expression supplied by static,
// trusted application code. Never use request input.
func TrustedMeasure(expression TrustedExpression) Measure {
	return Measure{kind: aggregateTrusted, trusted: expression}
}

// As sets the required output alias.
func (m Measure) As(alias Alias) Measure {
	m.alias = alias
	return m
}

// LOCF selects last-observation-carried-forward gap filling for this measure.
// treatNullAsMissing is passed to TimescaleDB's locf function.
func (m Measure) LOCF(treatNullAsMissing bool) Measure {
	m.fill = fillLOCF
	m.treatNullAsMissing = treatNullAsMissing
	return m
}

// Interpolate selects numeric linear interpolation for this measure. The
// aggregate expression must return a Timescale-supported numeric type.
func (m Measure) Interpolate() Measure {
	m.fill = fillInterpolate
	return m
}

// PreviousLookup sets the optional static lookup expression used before the
// first gap. LOCF expects a scalar; interpolation expects a (time,value) row.
func (m Measure) PreviousLookup(expression TrustedExpression) Measure {
	m.previous = expression
	return m
}

// NextLookup sets the optional static (time,value) lookup expression used by
// interpolation after the final known value.
func (m Measure) NextLookup(expression TrustedExpression) Measure {
	m.next = expression
	return m
}

func (m Measure) validate() error {
	if err := m.alias.validate(); err != nil {
		return err
	}
	switch m.kind {
	case aggregateCount:
		if len(m.columns) > 1 {
			return fmt.Errorf("timescaledb: count accepts zero or one column")
		}
	case aggregateSum, aggregateAvg, aggregateMin, aggregateMax:
		if len(m.columns) != 1 {
			return fmt.Errorf("timescaledb: aggregate requires one column")
		}
	case aggregateFirst, aggregateLast:
		if len(m.columns) != 2 {
			return fmt.Errorf("timescaledb: first/last requires value and time columns")
		}
	case aggregateTrusted:
		if err := validateTrusted("measure expression", m.trusted); err != nil {
			return err
		}
	default:
		return fmt.Errorf("timescaledb: unsupported measure")
	}
	for _, column := range m.columns {
		if err := column.validate(); err != nil {
			return err
		}
	}
	if m.previous != "" {
		if err := validateTrusted("previous lookup expression", m.previous); err != nil {
			return err
		}
		if m.fill == fillNull {
			return fmt.Errorf("timescaledb: previous lookup requires LOCF or interpolation")
		}
	}
	if m.next != "" {
		if err := validateTrusted("next lookup expression", m.next); err != nil {
			return err
		}
	}
	if m.next != "" && m.fill != fillInterpolate {
		return fmt.Errorf("timescaledb: next lookup is supported only for interpolation")
	}
	if m.fill != fillLOCF && m.treatNullAsMissing {
		return fmt.Errorf("timescaledb: treat_null_as_missing requires LOCF")
	}
	return nil
}

func (m Measure) aggregateSQL() (string, error) {
	if err := m.validate(); err != nil {
		return "", err
	}
	quoted := make([]string, len(m.columns))
	for index, column := range m.columns {
		value, err := column.quoted()
		if err != nil {
			return "", err
		}
		quoted[index] = value
	}
	switch m.kind {
	case aggregateCount:
		if len(quoted) == 0 {
			return "COUNT(*)", nil
		}
		return "COUNT(" + quoted[0] + ")", nil
	case aggregateSum:
		return "SUM(" + quoted[0] + ")", nil
	case aggregateAvg:
		return "AVG(" + quoted[0] + ")", nil
	case aggregateMin:
		return "MIN(" + quoted[0] + ")", nil
	case aggregateMax:
		return "MAX(" + quoted[0] + ")", nil
	case aggregateFirst:
		return "first(" + quoted[0] + ", " + quoted[1] + ")", nil
	case aggregateLast:
		return "last(" + quoted[0] + ", " + quoted[1] + ")", nil
	case aggregateTrusted:
		return strings.TrimSpace(string(m.trusted)), nil
	default:
		return "", fmt.Errorf("timescaledb: unsupported measure")
	}
}

func (m Measure) selectSQL(gapfill bool) (string, error) {
	aggregate, err := m.aggregateSQL()
	if err != nil {
		return "", err
	}
	if !gapfill && m.fill != fillNull {
		return "", fmt.Errorf("timescaledb: filled measure requires gapfill")
	}
	if gapfill {
		switch m.fill {
		case fillNull:
		case fillLOCF:
			parts := []string{aggregate}
			if m.previous != "" {
				parts = append(parts, "prev => ("+strings.TrimSpace(string(m.previous))+")")
			}
			if m.treatNullAsMissing {
				parts = append(parts, "treat_null_as_missing => TRUE")
			}
			aggregate = "locf(" + strings.Join(parts, ", ") + ")"
		case fillInterpolate:
			parts := []string{aggregate}
			if m.previous != "" {
				parts = append(parts, "prev => ("+strings.TrimSpace(string(m.previous))+")")
			}
			if m.next != "" {
				parts = append(parts, "next => ("+strings.TrimSpace(string(m.next))+")")
			}
			aggregate = "interpolate(" + strings.Join(parts, ", ") + ")"
		default:
			return "", fmt.Errorf("timescaledb: unsupported fill mode")
		}
	}
	alias, err := m.alias.quoted()
	if err != nil {
		return "", err
	}
	return aggregate + " AS " + alias, nil
}
