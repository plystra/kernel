package audit_test

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/plystra/kernel/audit"
)

func TestRuntimeIDsParseCanonicalValues(t *testing.T) {
	t.Parallel()

	const value = "0123456789abcdef0123456789abcdef"
	invocationID, err := audit.ParseInvocationID(value)
	if err != nil || !invocationID.Valid() || invocationID.String() != value {
		t.Fatalf("ParseInvocationID = %#v, %v", invocationID, err)
	}
	requestID, err := audit.ParseRequestID(value)
	if err != nil || !requestID.Valid() || requestID.String() != value {
		t.Fatalf("ParseRequestID = %#v, %v", requestID, err)
	}
	traceID, err := audit.ParseTraceID(value)
	if err != nil || !traceID.Valid() || traceID.String() != value {
		t.Fatalf("ParseTraceID = %#v, %v", traceID, err)
	}
}

func TestRuntimeIDConstructorsGenerateDistinctCanonicalValues(t *testing.T) {
	t.Parallel()

	const generations = 64
	seen := make(map[string]string, generations*3)
	for index := range generations {
		invocationID, err := audit.NewInvocationID()
		if err != nil {
			t.Fatalf("NewInvocationID: %v", err)
		}
		requestID, err := audit.NewRequestID()
		if err != nil {
			t.Fatalf("NewRequestID: %v", err)
		}
		traceID, err := audit.NewTraceID()
		if err != nil {
			t.Fatalf("NewTraceID: %v", err)
		}
		generated := []struct {
			kind  string
			value string
			valid bool
		}{
			{kind: "invocation", value: invocationID.String(), valid: invocationID.Valid()},
			{kind: "request", value: requestID.String(), valid: requestID.Valid()},
			{kind: "trace", value: traceID.String(), valid: traceID.Valid()},
		}
		for _, id := range generated {
			if !id.valid || len(id.value) != 32 || strings.ToLower(id.value) != id.value {
				t.Fatalf("generated %s ID = %q, valid %t", id.kind, id.value, id.valid)
			}
			label := id.kind + " " + strconv.Itoa(index)
			if previous, duplicate := seen[id.value]; duplicate {
				t.Fatalf("generated IDs collided: %s and %s", previous, label)
			}
			seen[id.value] = label
		}
	}
}

func TestRuntimeIDParsersRejectNonCanonicalValues(t *testing.T) {
	t.Parallel()

	for _, value := range []string{
		"",
		"0",
		strings.Repeat("0", 32),
		"0123456789abcdef0123456789abcde",
		"0123456789abcdef0123456789abcdef0",
		"0123456789ABCDEF0123456789ABCDEF",
		"g123456789abcdef0123456789abcdef",
		" 123456789abcdef0123456789abcdef",
	} {
		invocationID, invocationErr := audit.ParseInvocationID(value)
		requestID, requestErr := audit.ParseRequestID(value)
		traceID, traceErr := audit.ParseTraceID(value)
		if !errors.Is(invocationErr, audit.ErrInvalidRuntimeID) || invocationID.Valid() || invocationID.String() != "" {
			t.Fatalf("ParseInvocationID(%q) = %#v, %v", value, invocationID, invocationErr)
		}
		if !errors.Is(requestErr, audit.ErrInvalidRuntimeID) || requestID.Valid() || requestID.String() != "" {
			t.Fatalf("ParseRequestID(%q) = %#v, %v", value, requestID, requestErr)
		}
		if !errors.Is(traceErr, audit.ErrInvalidRuntimeID) || traceID.Valid() || traceID.String() != "" {
			t.Fatalf("ParseTraceID(%q) = %#v, %v", value, traceID, traceErr)
		}
	}
}

func TestZeroRuntimeIDsAreInvalid(t *testing.T) {
	t.Parallel()

	var invocationID audit.InvocationID
	var requestID audit.RequestID
	var traceID audit.TraceID
	if invocationID.Valid() || requestID.Valid() || traceID.Valid() {
		t.Fatal("a zero runtime ID is valid")
	}
	if invocationID.String() != "" || requestID.String() != "" || traceID.String() != "" {
		t.Fatalf("zero IDs = %q / %q / %q", invocationID, requestID, traceID)
	}
}

func FuzzRuntimeIDParsing(f *testing.F) {
	f.Add("0123456789abcdef0123456789abcdef")
	f.Add(strings.Repeat("0", 32))
	f.Add("bad")

	f.Fuzz(func(t *testing.T, value string) {
		invocationID, invocationErr := audit.ParseInvocationID(value)
		requestID, requestErr := audit.ParseRequestID(value)
		traceID, traceErr := audit.ParseTraceID(value)
		if (invocationErr == nil) != (requestErr == nil) || (requestErr == nil) != (traceErr == nil) {
			t.Fatalf("ID parsers disagree: %v / %v / %v", invocationErr, requestErr, traceErr)
		}
		if invocationErr != nil {
			if !errors.Is(invocationErr, audit.ErrInvalidRuntimeID) || !errors.Is(requestErr, audit.ErrInvalidRuntimeID) || !errors.Is(traceErr, audit.ErrInvalidRuntimeID) {
				t.Fatalf("unexpected errors: %v / %v / %v", invocationErr, requestErr, traceErr)
			}
			if invocationID.Valid() || requestID.Valid() || traceID.Valid() {
				t.Fatalf("invalid parsed IDs are valid: %#v / %#v / %#v", invocationID, requestID, traceID)
			}
			return
		}
		if !invocationID.Valid() || !requestID.Valid() || !traceID.Valid() || invocationID.String() != value || requestID.String() != value || traceID.String() != value {
			t.Fatalf("round trip = %q / %q / %q, want %q", invocationID, requestID, traceID, value)
		}
	})
}
