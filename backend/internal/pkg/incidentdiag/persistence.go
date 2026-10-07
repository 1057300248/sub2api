// Package incidentdiag supplies bounded, credential-free persistence diagnostics.
package incidentdiag

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
)

// ErrorClass deliberately excludes SQL text, driver Detail and arbitrary error
// strings: those can contain request data or credentials. SQLSTATE is bounded.
func ErrorClass(err error) (kind, state string) {
	if err == nil {
		return "none", ""
	}
	if errors.Is(err, context.Canceled) {
		return "canceled", ""
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline", ""
	}
	var sqlErr interface{ SQLState() string }
	if errors.As(err, &sqlErr) {
		value := sqlErr.SQLState()
		if len(value) == 5 && strings.IndexFunc(value, func(r rune) bool { return (r < '0' || r > '9') && (r < 'A' || r > 'Z') }) < 0 {
			state = value
		}
	}
	switch state {
	case "23503":
		return "foreign_key", state
	case "23505":
		return "unique_constraint", state
	case "23514":
		return "check_constraint", state
	case "40001", "40P01":
		return "transaction_retry", state
	default:
		return "persistence_error", state
	}
}

// RequestID preserves ordinary correlation IDs, hashing oversized/non-printable
// input instead of echoing arbitrary payloads into operational logs.
func RequestID(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= 128 && strings.IndexFunc(value, func(r rune) bool { return r < 33 || r > 126 }) < 0 {
		return value
	}
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:16])
}
