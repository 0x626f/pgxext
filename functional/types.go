// Package database provides PostgreSQL database-level object builders.
package functional

// Volatility is a PostgreSQL function volatility category.
type Volatility string

const (
	Volatile  Volatility = "VOLATILE"
	Stable    Volatility = "STABLE"
	Immutable Volatility = "IMMUTABLE"
)

// CheckOption controls view CHECK OPTION behavior.
type CheckOption string

const (
	LocalCheckOption    CheckOption = "LOCAL"
	CascadedCheckOption CheckOption = "CASCADED"
)
