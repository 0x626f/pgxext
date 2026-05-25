package notification

import (
	"fmt"
	"strings"
	"unicode"
)

func quoteIdentifier(identifier string) (string, error) {
	if identifier == "" {
		return "", fmt.Errorf("notification: empty identifier")
	}
	if strings.ContainsRune(identifier, 0) {
		return "", fmt.Errorf("notification: invalid identifier %q", identifier)
	}
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`, nil
}

func validateIdentifier(identifier string) error {
	if !isIdentifier(identifier) {
		return fmt.Errorf("notification: invalid identifier %q", identifier)
	}
	return nil
}

func quoteQualifiedIdentifier(identifier string) (string, error) {
	if identifier == "" {
		return "", fmt.Errorf("notification: empty identifier")
	}
	parts := strings.Split(identifier, ".")
	quoted := make([]string, len(parts))
	for i, part := range parts {
		if !isIdentifier(part) {
			return "", fmt.Errorf("notification: invalid identifier %q", identifier)
		}
		quoted[i] = `"` + strings.ReplaceAll(part, `"`, `""`) + `"`
	}
	return strings.Join(quoted, "."), nil
}

func sqlLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
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
