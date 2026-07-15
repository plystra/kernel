package audit_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/plystra/kernel/audit"
)

func TestInvocationOutcomesUseClosedTerminalStates(t *testing.T) {
	t.Parallel()

	succeeded := audit.NewSucceededOutcome()
	if !succeeded.Valid() || succeeded.Status() != audit.OutcomeSucceeded || succeeded.ErrorCode() != "" || succeeded.DetailCode() != "" {
		t.Fatalf("succeeded Outcome = %#v", succeeded)
	}

	for _, test := range []struct {
		code   audit.ErrorCode
		detail string
		status audit.OutcomeStatus
	}{
		{code: audit.ErrorInvalidArgument, detail: "contract.invalid_recipient", status: audit.OutcomeFailed},
		{code: audit.ErrorNotFound, status: audit.OutcomeFailed},
		{code: audit.ErrorConflict, status: audit.OutcomeFailed},
		{code: audit.ErrorDenied, detail: "authorization.policy_denied", status: audit.OutcomeDenied},
		{code: audit.ErrorUnauthenticated, detail: "authentication.required", status: audit.OutcomeFailed},
		{code: audit.ErrorUnavailable, status: audit.OutcomeFailed},
		{code: audit.ErrorTimeout, status: audit.OutcomeTimedOut},
		{code: audit.ErrorCancelled, status: audit.OutcomeCancelled},
		{code: audit.ErrorResultUnknown, detail: "transport.delivery_unknown", status: audit.OutcomeResultUnknown},
		{code: audit.ErrorInternal, status: audit.OutcomeFailed},
		{code: audit.ErrorVersionIncompatible, status: audit.OutcomeFailed},
	} {
		outcome, err := audit.NewErrorOutcome(test.code, test.detail)
		if err != nil {
			t.Fatalf("NewErrorOutcome(%q): %v", test.code, err)
		}
		if !outcome.Valid() || outcome.Status() != test.status || outcome.Status().String() != string(test.status) || outcome.ErrorCode() != test.code || outcome.DetailCode() != test.detail {
			t.Fatalf("NewErrorOutcome(%q) = %#v", test.code, outcome)
		}
	}
}

func TestInvocationOutcomeRejectsUnsafeOrContradictoryFailures(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name   string
		code   audit.ErrorCode
		detail string
	}{
		{name: "missing error code"},
		{name: "unknown error code", code: "provider_failed"},
		{name: "denial without detail", code: audit.ErrorDenied},
		{name: "uppercase detail", code: audit.ErrorInternal, detail: "Internal.Failed"},
		{name: "hyphenated detail", code: audit.ErrorInternal, detail: "internal-failed"},
		{name: "leading separator", code: audit.ErrorInternal, detail: ".internal"},
		{name: "trailing separator", code: audit.ErrorInternal, detail: "internal."},
		{name: "repeated separator", code: audit.ErrorInternal, detail: "internal..failed"},
		{name: "repeated underscore", code: audit.ErrorInternal, detail: "internal__failed"},
		{name: "trailing underscore", code: audit.ErrorInternal, detail: "internal_"},
		{name: "whitespace", code: audit.ErrorInternal, detail: "internal failed"},
		{name: "oversized detail", code: audit.ErrorInternal, detail: strings.Repeat("a", audit.MaximumDetailCodeSize+1)},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			outcome, err := audit.NewErrorOutcome(test.code, test.detail)
			if !errors.Is(err, audit.ErrInvalidOutcome) || outcome.Valid() {
				t.Fatalf("NewErrorOutcome = %#v, %v", outcome, err)
			}
			if err != nil && err.Error() != audit.ErrInvalidOutcome.Error() {
				t.Fatalf("error exposed rejected input: %v", err)
			}
		})
	}
}

func TestZeroInvocationOutcomeFailsClosed(t *testing.T) {
	t.Parallel()

	var outcome audit.Outcome
	if outcome.Valid() || outcome.Status().Valid() || outcome.ErrorCode().Valid() || outcome.DetailCode() != "" {
		t.Fatalf("zero Outcome = %#v", outcome)
	}
	for _, status := range []audit.OutcomeStatus{"", "started", "success", "FAILED"} {
		if status.Valid() || status.String() != "" {
			t.Fatalf("invalid OutcomeStatus %q accepted", status)
		}
	}
}

func FuzzInvocationErrorOutcome(f *testing.F) {
	f.Add("denied", "authorization.policy_denied")
	f.Add("timeout", "")
	f.Add("provider_failed", "sensitive failure")
	f.Fuzz(func(t *testing.T, code, detail string) {
		outcome, err := audit.NewErrorOutcome(audit.ErrorCode(code), detail)
		if err != nil {
			if !errors.Is(err, audit.ErrInvalidOutcome) || outcome.Valid() {
				t.Fatalf("rejected Outcome = %#v, %v", outcome, err)
			}
			return
		}
		if !outcome.Valid() || outcome.ErrorCode().String() != code || outcome.DetailCode() != detail || !outcome.Status().Valid() {
			t.Fatalf("accepted Outcome = %#v", outcome)
		}
	})
}
