package invocation_test

import (
	"testing"

	"github.com/plystra/kernel/invocation"
)

func TestErrorCodesHaveStableUniqueValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		code invocation.ErrorCode
		want string
	}{
		{invocation.ErrorInvalidArgument, "invalid_argument"},
		{invocation.ErrorNotFound, "not_found"},
		{invocation.ErrorConflict, "conflict"},
		{invocation.ErrorDenied, "denied"},
		{invocation.ErrorUnauthenticated, "unauthenticated"},
		{invocation.ErrorUnavailable, "unavailable"},
		{invocation.ErrorTimeout, "timeout"},
		{invocation.ErrorCancelled, "cancelled"},
		{invocation.ErrorResultUnknown, "result_unknown"},
		{invocation.ErrorInternal, "internal"},
		{invocation.ErrorVersionIncompatible, "version_incompatible"},
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

	for _, code := range []invocation.ErrorCode{"", "unknown", "INTERNAL", "not-found"} {
		if code.Valid() {
			t.Fatalf("ErrorCode %q is valid", code)
		}
	}
}
