package functional

import (
	"fmt"
	"strings"
	"unicode"
)

func quoteIdentifier(identifier string) (string, error) {
	if identifier == "" {
		return "", fmt.Errorf("database: empty identifier")
	}
	if strings.ContainsRune(identifier, 0) {
		return "", fmt.Errorf("database: invalid identifier %q", identifier)
	}
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`, nil
}

func quoteQualifiedIdentifier(identifier string) (string, error) {
	if identifier == "" {
		return "", fmt.Errorf("database: empty identifier")
	}
	parts := strings.Split(identifier, ".")
	quoted := make([]string, len(parts))
	for i, part := range parts {
		if !isIdentifier(part) {
			return "", fmt.Errorf("database: invalid identifier %q", identifier)
		}
		quoted[i] = `"` + strings.ReplaceAll(part, `"`, `""`) + `"`
	}
	return strings.Join(quoted, "."), nil
}

func validateIdentifier(identifier string) error {
	if !isIdentifier(identifier) {
		return fmt.Errorf("database: invalid identifier %q", identifier)
	}
	return nil
}

func validateSQLFragment(kind, value string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("database: empty %s", kind)
	}
	if strings.ContainsRune(value, 0) {
		return fmt.Errorf("database: invalid %s", kind)
	}
	return nil
}

func validateDefinitionFragment(kind, value string) error {
	if err := validateSQLFragment(kind, value); err != nil {
		return err
	}
	if strings.Contains(value, ";") || strings.Contains(value, "--") || strings.Contains(value, "/*") || strings.Contains(value, "*/") {
		return fmt.Errorf("database: invalid %s", kind)
	}
	return nil
}

func isIdentifier(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if r == '_' || unicode.IsLetter(r) {
			continue
		}
		if i > 0 && unicode.IsDigit(r) {
			continue
		}
		return false
	}
	return true
}
