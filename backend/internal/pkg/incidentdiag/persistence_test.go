package incidentdiag

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

type databaseError struct{ state string }

func (e databaseError) Error() string    { return "private SQL detail and secret" }
func (e databaseError) SQLState() string { return e.state }

func TestClineIncidentPersistenceClassification(t *testing.T) {
	for _, tc := range []struct {
		err         error
		kind, state string
	}{
		{fmt.Errorf("wrapped: %w", databaseError{"23503"}), "foreign_key", "23503"},
		{databaseError{"40001"}, "transaction_retry", "40001"},
		{databaseError{"credential-shaped-state"}, "persistence_error", ""},
		{context.Canceled, "canceled", ""}, {context.DeadlineExceeded, "deadline", ""},
		{errors.New("private credential detail"), "persistence_error", ""},
	} {
		kind, state := ErrorClass(tc.err)
		if kind != tc.kind || state != tc.state {
			t.Fatalf("%s %s", kind, state)
		}
	}
}
func TestClineIncidentRequestIDBounds(t *testing.T) {
	if RequestID("req-123") != "req-123" {
		t.Fatal("ordinary ID changed")
	}
	for _, value := range []string{strings.Repeat("x", 1024), "req\nprivate"} {
		got := RequestID(value)
		if len(got) != 39 || !strings.HasPrefix(got, "sha256:") {
			t.Fatalf("bad bounded ID %q", got)
		}
	}
}
