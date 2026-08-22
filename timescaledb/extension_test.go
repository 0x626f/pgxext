package timescaledb

import (
	"errors"
	"testing"
)

func TestCreateExtensionSQL(t *testing.T) {
	if got, want := CreateExtensionSQL(), "CREATE EXTENSION IF NOT EXISTS timescaledb;"; got != want {
		t.Fatalf("CreateExtensionSQL() = %q, want %q", got, want)
	}
}

func TestParseVersion(t *testing.T) {
	for _, test := range []struct {
		input               string
		major, minor, patch int
	}{
		{"2.20.0", 2, 20, 0},
		{"2.20.2-pg17", 2, 20, 2},
		{"2.21+cloud", 2, 21, 0},
	} {
		major, minor, patch, err := parseVersion(test.input)
		if err != nil || major != test.major || minor != test.minor || patch != test.patch {
			t.Errorf("parseVersion(%q) = %d.%d.%d, %v", test.input, major, minor, patch, err)
		}
	}
	for _, input := range []string{"", "2", "2.20.0.1", "two.20.0", "2.-1.0"} {
		if _, _, _, err := parseVersion(input); err == nil {
			t.Errorf("parseVersion(%q) unexpectedly succeeded", input)
		}
	}
}

func TestCapabilityErrorStableUnwrap(t *testing.T) {
	err := &CapabilityError{
		Feature: "columnstore", Version: "2.19.3", Required: ">=2.20.0,<3.0.0",
		unsupportedVersion: true,
	}
	if !errors.Is(err, ErrCapabilityUnavailable) || !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("capability error did not preserve stable sentinels: %v", err)
	}
	editionErr := &CapabilityError{Feature: "columnstore", Version: "2.20.2", Required: "Community/Cloud"}
	if !errors.Is(editionErr, ErrCapabilityUnavailable) || errors.Is(editionErr, ErrUnsupportedVersion) {
		t.Fatalf("edition capability error has incorrect stable sentinels: %v", editionErr)
	}
}
