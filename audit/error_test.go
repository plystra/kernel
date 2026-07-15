package audit_test

import (
	"testing"

	"github.com/plystra/kernel/audit"
)

func TestErrorCodesHaveStableUniqueValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		code audit.ErrorCode
		want string
	}{
		{audit.ErrorInvalidArgument, "invalid_argument"},
		{audit.ErrorNotFound, "not_found"},
		{audit.ErrorConflict, "conflict"},
		{audit.ErrorDenied, "denied"},
		{audit.ErrorUnauthenticated, "unauthenticated"},
		{audit.ErrorUnavailable, "unavailable"},
		{audit.ErrorTimeout, "timeout"},
		{audit.ErrorCancelled, "cancelled"},
		{audit.ErrorResultUnknown, "result_unknown"},
		{audit.ErrorInternal, "internal"},
		{audit.ErrorVersionIncompatible, "version_incompatible"},
	}
	seen := make(map[string]struct{}, len(tests))
	for _, test := range tests {
		if !test.code.Valid() || test.code.String() != test.want {
			t.Fatalf("ErrorCode %q = %q, valid %t", test.code, test.code.String(), test.code.Valid())
		}
		if _, duplicate := seen[test.code.String()]; duplicate {
			t.Fatalf("duplicate ErrorCode value %q", test.code)
		}
		seen[test.code.String()] = struct{}{}
	}
}

func TestUnknownErrorCodesAreInvalid(t *testing.T) {
	t.Parallel()

	for _, code := range []audit.ErrorCode{"", "unknown", "INTERNAL", "not-found"} {
		if code.Valid() {
			t.Fatalf("ErrorCode %q is valid", code)
		}
	}
}
