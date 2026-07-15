package invocation

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/kernel/audit"
)

func TestNewErrorSupportsEveryStandardClass(t *testing.T) {
	t.Parallel()

	for _, code := range []audit.ErrorCode{
		audit.ErrorInvalidArgument,
		audit.ErrorNotFound,
		audit.ErrorConflict,
		audit.ErrorDenied,
		audit.ErrorUnauthenticated,
		audit.ErrorUnavailable,
		audit.ErrorTimeout,
		audit.ErrorCancelled,
		audit.ErrorResultUnknown,
		audit.ErrorInternal,
		audit.ErrorVersionIncompatible,
	} {
		boundary, err := NewError(code, "contract.failure")
		if err != nil {
			t.Fatalf("NewError(%q): %v", code, err)
		}
		want := "capability invocation failed: " + code.String() + ": contract.failure"
		if !boundary.valid() || boundary.Code() != code || boundary.DetailCode() != "contract.failure" || boundary.Error() != want {
			t.Fatalf("NewError(%q) = %#v / %q", code, boundary, boundary.Error())
		}
	}
}

func TestNewErrorAllowsEmptyDetailExceptForDenial(t *testing.T) {
	t.Parallel()

	for _, code := range []audit.ErrorCode{
		audit.ErrorInvalidArgument,
		audit.ErrorNotFound,
		audit.ErrorConflict,
		audit.ErrorUnauthenticated,
		audit.ErrorUnavailable,
		audit.ErrorTimeout,
		audit.ErrorCancelled,
		audit.ErrorResultUnknown,
		audit.ErrorInternal,
		audit.ErrorVersionIncompatible,
	} {
		boundary, err := NewError(code, "")
		if err != nil || !boundary.valid() || boundary.DetailCode() != "" {
			t.Fatalf("NewError(%q, empty) = %#v, %v", code, boundary, err)
		}
	}
	if boundary, err := NewError(audit.ErrorDenied, ""); !errors.Is(err, ErrInvalidError) || boundary != nil {
		t.Fatalf("empty denial = %#v, %v", boundary, err)
	}
}

func TestNewErrorAcceptsCanonicalDetailCodes(t *testing.T) {
	t.Parallel()

	for _, detail := range []string{
		"a",
		"invalid_recipient",
		"authorization.policy_denied",
		"contract.v2_error",
		strings.Repeat("a", maximumDetailCodeSize),
	} {
		boundary, err := NewError(audit.ErrorInternal, detail)
		if err != nil || !boundary.valid() || boundary.DetailCode() != detail {
			t.Fatalf("NewError(detail %q) = %#v, %v", detail, boundary, err)
		}
	}
}

func TestNewErrorRejectsInvalidCodesAndDetails(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		code   audit.ErrorCode
		detail string
	}{
		{code: ""},
		{code: "unknown"},
		{code: audit.ErrorInternal, detail: "Uppercase"},
		{code: audit.ErrorInternal, detail: "bad-code"},
		{code: audit.ErrorInternal, detail: "bad__code"},
		{code: audit.ErrorInternal, detail: "bad_code_"},
		{code: audit.ErrorInternal, detail: ".bad"},
		{code: audit.ErrorInternal, detail: "bad."},
		{code: audit.ErrorInternal, detail: "bad..code"},
		{code: audit.ErrorInternal, detail: "bad code"},
		{code: audit.ErrorInternal, detail: "秘密"},
		{code: audit.ErrorInternal, detail: strings.Repeat("a", maximumDetailCodeSize+1)},
	} {
		boundary, err := NewError(test.code, test.detail)
		if !errors.Is(err, ErrInvalidError) || boundary != nil {
			t.Fatalf("NewError(%q, %q) = %#v, %v", test.code, test.detail, boundary, err)
		}
	}
}

func TestErrorPreservesOnlySafeContextIdentities(t *testing.T) {
	t.Parallel()

	tests := []struct {
		code   audit.ErrorCode
		target error
	}{
		{audit.ErrorTimeout, context.DeadlineExceeded},
		{audit.ErrorCancelled, context.Canceled},
	}
	for _, test := range tests {
		boundary, err := NewError(test.code, "runtime.limit")
		if err != nil {
			t.Fatalf("NewError(%q): %v", test.code, err)
		}
		if !errors.Is(boundary, test.target) {
			t.Fatalf("NewError(%q) does not match %v", test.code, test.target)
		}
		other := context.Canceled
		if test.target == context.Canceled {
			other = context.DeadlineExceeded
		}
		if errors.Is(boundary, other) || errors.Is(boundary, errors.New("private")) {
			t.Fatalf("NewError(%q) matched an unrelated error", test.code)
		}
	}
	internal, err := NewError(audit.ErrorInternal, "")
	if err != nil || errors.Is(internal, context.Canceled) || errors.Is(internal, context.DeadlineExceeded) {
		t.Fatalf("internal error exposes a context identity: %#v, %v", internal, err)
	}
}

func TestErrorExposesNoMutableOrFreeFormState(t *testing.T) {
	t.Parallel()

	errorType := reflect.TypeFor[Error]()
	for index := range errorType.NumField() {
		if field := errorType.Field(index); field.IsExported() {
			t.Fatalf("Error field %q exposes mutable state", field.Name)
		}
	}
	boundary, err := NewError(audit.ErrorConflict, "document.version_conflict")
	if err != nil {
		t.Fatalf("NewError: %v", err)
	}
	if strings.Contains(boundary.Error(), "provider") || boundary.Error() != "capability invocation failed: conflict: document.version_conflict" {
		t.Fatalf("unsafe Error text = %q", boundary.Error())
	}
}

func TestNilAndZeroErrorAreSafe(t *testing.T) {
	t.Parallel()

	var nilError *Error
	if nilError.valid() || nilError.Error() != ErrInvalidError.Error() || nilError.Code() != "" || nilError.DetailCode() != "" || errors.Is(nilError, context.Canceled) {
		t.Fatal("nil Error behavior is unsafe")
	}
	zero := &Error{}
	if zero.valid() || zero.Error() != ErrInvalidError.Error() || zero.Code() != "" || zero.DetailCode() != "" || errors.Is(zero, context.DeadlineExceeded) {
		t.Fatal("zero Error behavior is unsafe")
	}
}

func FuzzNewError(f *testing.F) {
	f.Add("internal", "")
	f.Add("denied", "authorization.policy_denied")
	f.Add("timeout", "runtime.deadline")
	f.Add("unknown", "private details")

	f.Fuzz(func(t *testing.T, code, detail string) {
		boundary, err := NewError(audit.ErrorCode(code), detail)
		if err != nil {
			if !errors.Is(err, ErrInvalidError) || boundary != nil {
				t.Fatalf("NewError(%q, %q) = %#v, %v", code, detail, boundary, err)
			}
			return
		}
		if !boundary.valid() || boundary.Code().String() != code || boundary.DetailCode() != detail {
			t.Fatalf("NewError(%q, %q) = %#v", code, detail, boundary)
		}
		if !strings.HasPrefix(boundary.Error(), "capability invocation failed: ") {
			t.Fatalf("Error text = %q", boundary.Error())
		}
	})
}
