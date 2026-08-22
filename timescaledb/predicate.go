package timescaledb

import (
	"fmt"
	"strings"
)

type predicateOperator uint8

const (
	predicateEqual predicateOperator = iota + 1
	predicateNotEqual
	predicateLess
	predicateLessOrEqual
	predicateGreater
	predicateGreaterOrEqual
	predicateIn
	predicateNotIn
	predicateIsNull
	predicateIsNotNull
)

// Predicate is a parameterized, allow-listed frame-query condition.
type Predicate struct {
	column Column
	op     predicateOperator
	values []any
}

func Equal(column Column, value any) Predicate {
	return Predicate{column: column, op: predicateEqual, values: []any{value}}
}

func NotEqual(column Column, value any) Predicate {
	return Predicate{column: column, op: predicateNotEqual, values: []any{value}}
}

func LessThan(column Column, value any) Predicate {
	return Predicate{column: column, op: predicateLess, values: []any{value}}
}

func LessThanOrEqual(column Column, value any) Predicate {
	return Predicate{column: column, op: predicateLessOrEqual, values: []any{value}}
}

func GreaterThan(column Column, value any) Predicate {
	return Predicate{column: column, op: predicateGreater, values: []any{value}}
}

func GreaterThanOrEqual(column Column, value any) Predicate {
	return Predicate{column: column, op: predicateGreaterOrEqual, values: []any{value}}
}

func In(column Column, values ...any) Predicate {
	return Predicate{column: column, op: predicateIn, values: append([]any(nil), values...)}
}

func NotIn(column Column, values ...any) Predicate {
	return Predicate{column: column, op: predicateNotIn, values: append([]any(nil), values...)}
}

func IsNull(column Column) Predicate {
	return Predicate{column: column, op: predicateIsNull}
}

func IsNotNull(column Column) Predicate {
	return Predicate{column: column, op: predicateIsNotNull}
}

func (p Predicate) sql(args *[]any) (string, error) {
	column, err := p.column.quoted()
	if err != nil {
		return "", err
	}
	switch p.op {
	case predicateEqual, predicateNotEqual, predicateLess, predicateLessOrEqual, predicateGreater, predicateGreaterOrEqual:
		if len(p.values) != 1 {
			return "", fmt.Errorf("timescaledb: predicate requires exactly one value")
		}
		operator := map[predicateOperator]string{
			predicateEqual:          "=",
			predicateNotEqual:       "!=",
			predicateLess:           "<",
			predicateLessOrEqual:    "<=",
			predicateGreater:        ">",
			predicateGreaterOrEqual: ">=",
		}[p.op]
		*args = append(*args, p.values[0])
		return fmt.Sprintf("%s %s $%d", column, operator, len(*args)), nil
	case predicateIn, predicateNotIn:
		if len(p.values) == 0 {
			if p.op == predicateIn {
				return "FALSE", nil
			}
			return "TRUE", nil
		}
		placeholders := make([]string, len(p.values))
		for index, value := range p.values {
			*args = append(*args, value)
			placeholders[index] = fmt.Sprintf("$%d", len(*args))
		}
		operator := "IN"
		if p.op == predicateNotIn {
			operator = "NOT IN"
		}
		return fmt.Sprintf("%s %s (%s)", column, operator, strings.Join(placeholders, ", ")), nil
	case predicateIsNull:
		if len(p.values) != 0 {
			return "", fmt.Errorf("timescaledb: IS NULL predicate accepts no values")
		}
		return column + " IS NULL", nil
	case predicateIsNotNull:
		if len(p.values) != 0 {
			return "", fmt.Errorf("timescaledb: IS NOT NULL predicate accepts no values")
		}
		return column + " IS NOT NULL", nil
	default:
		return "", fmt.Errorf("timescaledb: unsupported predicate operator")
	}
}
