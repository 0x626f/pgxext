package timescaledb

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/0x626f/pgxext"
)

const (
	// MinimumVersion is the oldest supported TimescaleDB version.
	MinimumVersion = "2.20.0"
	// SupportedPostgreSQL documents PostgreSQL majors supported across the
	// package's TimescaleDB 2.x baseline. PostgreSQL 18 requires TimescaleDB
	// 2.23 or newer.
	SupportedPostgreSQL = "15, 16, 17, 18 (TimescaleDB 2.23+ required for PostgreSQL 18)"
)

// Capabilities describes the installed TimescaleDB extension.
type Capabilities struct {
	Installed                 bool
	Version                   string
	Compatible                bool
	ModernHypertableCreate    bool
	Gapfill                   bool
	ContinuousAggregates      bool
	RealTimeAggregates        bool
	RefreshPolicies           bool
	ManualRefresh             bool
	RetentionPolicies         bool
	Columnstore               bool
	RefreshPolicyBatching     bool
	RefreshNewestFirst        bool
	TIMESTAMPTZTimeDimensions bool
	IntegerTimeDimensions     bool
}

// CreateExtensionSQL returns migration-safe SQL. The package never executes it
// implicitly and intentionally provides no DROP EXTENSION rollback.
func CreateExtensionSQL() string {
	return "CREATE EXTENSION IF NOT EXISTS timescaledb;"
}

// CreateExtension explicitly installs TimescaleDB in the current database.
// Applications should call this only from an authorized bootstrap or migration.
func CreateExtension(ctx context.Context, db *pgxext.DataSource) error {
	if ctx == nil {
		return fmt.Errorf("timescaledb: create extension: nil context")
	}
	if db == nil {
		return fmt.Errorf("timescaledb: create extension: nil DataSource")
	}
	if _, err := db.Exec(ctx, CreateExtensionSQL()); err != nil {
		return fmt.Errorf("timescaledb: create extension: %w", err)
	}
	return nil
}

// ExtensionInstalled reports whether TimescaleDB is installed in this database.
func ExtensionInstalled(ctx context.Context, db *pgxext.DataSource) (bool, error) {
	if ctx == nil {
		return false, fmt.Errorf("timescaledb: inspect extension: nil context")
	}
	if db == nil {
		return false, fmt.Errorf("timescaledb: inspect extension: nil DataSource")
	}
	rows, err := db.Query(ctx, `SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'timescaledb')`)
	if err != nil {
		return false, fmt.Errorf("timescaledb: inspect extension installation: %w", err)
	}
	defer closeRows(rows)
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return false, fmt.Errorf("timescaledb: inspect extension installation: %w", err)
		}
		return false, fmt.Errorf("timescaledb: inspect extension installation: no result")
	}
	var installed bool
	if err := rows.Scan(&installed); err != nil {
		return false, fmt.Errorf("timescaledb: inspect extension installation: %w", err)
	}
	return installed, nil
}

// InstalledVersion returns the extension version or ErrExtensionNotInstalled.
func InstalledVersion(ctx context.Context, db *pgxext.DataSource) (string, error) {
	if ctx == nil {
		return "", fmt.Errorf("timescaledb: read extension version: nil context")
	}
	if db == nil {
		return "", fmt.Errorf("timescaledb: read extension version: nil DataSource")
	}
	rows, err := db.Query(ctx, `SELECT extversion FROM pg_extension WHERE extname = 'timescaledb'`)
	if err != nil {
		return "", fmt.Errorf("timescaledb: read extension version: %w", err)
	}
	defer closeRows(rows)
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return "", fmt.Errorf("timescaledb: read extension version: %w", err)
		}
		return "", fmt.Errorf("timescaledb: read extension version: %w", ErrExtensionNotInstalled)
	}
	var version string
	if err := rows.Scan(&version); err != nil {
		return "", fmt.Errorf("timescaledb: read extension version: %w", err)
	}
	return version, nil
}

// InspectCapabilities validates the installed extension against the supported
// 2.x compatibility baseline. Unsupported versions return both the result and
// an error wrapping ErrUnsupportedVersion.
func InspectCapabilities(ctx context.Context, db *pgxext.DataSource) (Capabilities, error) {
	version, err := InstalledVersion(ctx, db)
	if err != nil {
		return Capabilities{}, err
	}
	major, minor, _, err := parseVersion(version)
	if err != nil {
		return Capabilities{Installed: true, Version: version}, fmt.Errorf("timescaledb: parse installed version %q: %w", version, ErrUnsupportedVersion)
	}
	compatible := major == 2 && minor >= 20
	capabilities := Capabilities{
		Installed:                 true,
		Version:                   version,
		Compatible:                compatible,
		ModernHypertableCreate:    compatible,
		TIMESTAMPTZTimeDimensions: compatible,
		IntegerTimeDimensions:     false,
	}
	if !compatible {
		return capabilities, fmt.Errorf("timescaledb: version %s is outside supported range >=%s,<3.0.0: %w", version, MinimumVersion, ErrUnsupportedVersion)
	}
	if err := inspectFeatureAvailability(ctx, db, &capabilities); err != nil {
		return capabilities, err
	}
	return capabilities, nil
}

func inspectFeatureAvailability(ctx context.Context, db *pgxext.DataSource, capabilities *Capabilities) error {
	rows, err := db.Query(ctx, `
WITH extension_functions AS (
  SELECT p.proname
  FROM pg_catalog.pg_proc p
  JOIN pg_catalog.pg_depend d
    ON d.classid = 'pg_catalog.pg_proc'::regclass
   AND d.objid = p.oid
   AND d.refclassid = 'pg_catalog.pg_extension'::regclass
   AND d.deptype = 'e'
  JOIN pg_catalog.pg_extension e ON e.oid = d.refobjid
  WHERE e.extname = 'timescaledb'
)
SELECT EXISTS (SELECT 1 FROM extension_functions WHERE proname = 'time_bucket_gapfill'),
       EXISTS (SELECT 1 FROM extension_functions WHERE proname = 'add_continuous_aggregate_policy'),
       EXISTS (SELECT 1 FROM extension_functions WHERE proname = 'refresh_continuous_aggregate'),
       EXISTS (SELECT 1 FROM extension_functions WHERE proname = 'add_retention_policy'),
       EXISTS (SELECT 1 FROM extension_functions WHERE proname = 'add_columnstore_policy')`)
	if err != nil {
		return fmt.Errorf("timescaledb: inspect installed feature capabilities: %w", err)
	}
	defer closeRows(rows)
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return fmt.Errorf("timescaledb: inspect installed feature capabilities: %w", err)
		}
		return fmt.Errorf("timescaledb: inspect installed feature capabilities: no result")
	}
	if err := rows.Scan(
		&capabilities.Gapfill,
		&capabilities.RefreshPolicies,
		&capabilities.ManualRefresh,
		&capabilities.RetentionPolicies,
		&capabilities.Columnstore,
	); err != nil {
		return fmt.Errorf("timescaledb: inspect installed feature capabilities: %w", err)
	}
	capabilities.ContinuousAggregates = capabilities.RefreshPolicies && capabilities.ManualRefresh
	capabilities.RealTimeAggregates = capabilities.ContinuousAggregates
	capabilities.RefreshPolicyBatching = capabilities.RefreshPolicies
	capabilities.RefreshNewestFirst = capabilities.RefreshPolicies
	return nil
}

func requireCapability(ctx context.Context, db *pgxext.DataSource, feature string) (Capabilities, error) {
	capabilities, err := InspectCapabilities(ctx, db)
	if err != nil {
		if errors.Is(err, ErrUnsupportedVersion) {
			return capabilities, &CapabilityError{
				Feature: feature, Version: capabilities.Version, Required: ">=2.20.0,<3.0.0",
				unsupportedVersion: true,
			}
		}
		return capabilities, err
	}
	available := true
	required := ">=2.20.0,<3.0.0"
	switch feature {
	case "columnstore", "columnstore policy":
		available = capabilities.Columnstore
		required = ">=2.20.0,<3.0.0 with stable columnstore support (Community/Cloud where applicable)"
	case "modern hypertable CREATE TABLE":
		available = capabilities.ModernHypertableCreate
	}
	if !available {
		return capabilities, &CapabilityError{Feature: feature, Version: capabilities.Version, Required: required}
	}
	return capabilities, nil
}

func parseVersion(version string) (int, int, int, error) {
	base := version
	if index := strings.IndexAny(base, "-+"); index >= 0 {
		base = base[:index]
	}
	parts := strings.Split(base, ".")
	if len(parts) < 2 || len(parts) > 3 {
		return 0, 0, 0, fmt.Errorf("invalid semantic version")
	}
	values := [3]int{}
	for index, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil || value < 0 {
			return 0, 0, 0, fmt.Errorf("invalid semantic version")
		}
		values[index] = value
	}
	return values[0], values[1], values[2], nil
}
