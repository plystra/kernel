package invocation

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/plystra/kernel/audit"
)

type runtimeContextValueKey struct{}

func TestEnterInvocationContextStartsIdentityNeutralRoot(t *testing.T) {
	t.Parallel()

	parent := context.WithValue(context.Background(), runtimeContextValueKey{}, "preserved")
	invocationID := testInvocationID(t, "11111111111111111111111111111111")
	const timeout = time.Minute
	earliest := time.Now().Add(timeout)
	entered, cleanup, err := enterInvocationContext(parent, invocationID, timeout)
	latest := time.Now().Add(timeout)
	if err != nil {
		t.Fatalf("enterInvocationContext: %v", err)
	}
	defer cleanup()

	if entered.Value(runtimeContextValueKey{}) != "preserved" {
		t.Fatal("entered context lost an ordinary parent value")
	}
	current, exists := Current(entered)
	if !exists || !current.RequestID().Valid() || !current.TraceID().Valid() ||
		current.RequestID().String() == current.TraceID().String() || current.InvocationID() != invocationID ||
		current.ParentInvocationID().Valid() {
		t.Fatalf("root invocation context = %#v, exists %t", current, exists)
	}
	deadline, hasDeadline := entered.Deadline()
	if !hasDeadline || deadline.Before(earliest) || deadline.After(latest) || !current.Deadline().Equal(deadline) {
		t.Fatalf("root deadline = %v / %v, want between %v and %v", deadline, current.Deadline(), earliest, latest)
	}
	requestID, requestExists := RequestID(entered)
	traceID, traceExists := TraceID(entered)
	if !requestExists || !traceExists || requestID != current.RequestID() || traceID != current.TraceID() {
		t.Fatalf("public IDs = %q/%t, %q/%t", requestID, requestExists, traceID, traceExists)
	}
	frame, frameExists := runtimeFrameFrom(entered)
	if !frameExists || frame.authority != parent || frame.parentID.Valid() {
		t.Fatalf("runtime frame = %#v, exists %t", frame, frameExists)
	}
	if _, parentExists := Current(parent); parentExists {
		t.Fatal("enterInvocationContext mutated its parent")
	}
}

func TestEnterInvocationContextPreservesNestedAncestry(t *testing.T) {
	t.Parallel()

	outerID := testInvocationID(t, "22222222222222222222222222222222")
	innerID := testInvocationID(t, "33333333333333333333333333333333")
	outer, cleanupOuter, err := enterInvocationContext(context.Background(), outerID, time.Minute)
	if err != nil {
		t.Fatalf("enter outer: %v", err)
	}
	defer cleanupOuter()
	outerCurrent, exists := Current(outer)
	if !exists {
		t.Fatal("outer Current missing")
	}

	inner, cleanupInner, err := enterInvocationContext(outer, innerID, 2*time.Minute)
	if err != nil {
		t.Fatalf("enter inner: %v", err)
	}
	defer cleanupInner()
	innerCurrent, exists := Current(inner)
	if !exists || innerCurrent.RequestID() != outerCurrent.RequestID() || innerCurrent.TraceID() != outerCurrent.TraceID() ||
		innerCurrent.InvocationID() != innerID || innerCurrent.ParentInvocationID() != outerID ||
		!innerCurrent.Deadline().Equal(outerCurrent.Deadline()) {
		t.Fatalf("nested contexts = outer %#v / inner %#v", outerCurrent, innerCurrent)
	}
	if unchanged, _ := Current(outer); unchanged != outerCurrent {
		t.Fatalf("nested entry mutated outer context: %#v != %#v", unchanged, outerCurrent)
	}
}

func TestEnterInvocationContextChoosesEarliestDeadline(t *testing.T) {
	t.Parallel()

	t.Run("configured default", func(t *testing.T) {
		const timeout = time.Minute
		earliest := time.Now().Add(timeout)
		entered, cleanup, err := enterInvocationContext(context.Background(), testInvocationID(t, "44444444444444444444444444444444"), timeout)
		latest := time.Now().Add(timeout)
		if err != nil {
			t.Fatalf("enterInvocationContext: %v", err)
		}
		defer cleanup()
		deadline, exists := entered.Deadline()
		if !exists || deadline.Before(earliest) || deadline.After(latest) {
			t.Fatalf("configured deadline = %v, want between %v and %v", deadline, earliest, latest)
		}
	})

	t.Run("ordinary caller", func(t *testing.T) {
		callerDeadline := time.Now().Add(time.Minute)
		caller, cancelCaller := context.WithDeadline(context.Background(), callerDeadline)
		defer cancelCaller()
		entered, cleanup, err := enterInvocationContext(caller, testInvocationID(t, "55555555555555555555555555555555"), time.Hour)
		if err != nil {
			t.Fatalf("enterInvocationContext: %v", err)
		}
		defer cleanup()
		deadline, exists := entered.Deadline()
		if !exists || !deadline.Equal(callerDeadline) {
			t.Fatalf("ordinary caller deadline = %v, want %v", deadline, callerDeadline)
		}
	})

	t.Run("nested frame", func(t *testing.T) {
		authorityDeadline := time.Now().Add(time.Minute)
		authority, cancelAuthority := context.WithDeadline(context.Background(), authorityDeadline)
		defer cancelAuthority()
		outer, cleanupOuter, err := enterInvocationContext(authority, testInvocationID(t, "66666666666666666666666666666666"), time.Hour)
		if err != nil {
			t.Fatalf("enter outer: %v", err)
		}
		defer cleanupOuter()
		detached := context.WithoutCancel(outer)
		if _, exists := detached.Deadline(); exists {
			t.Fatal("test context retained ordinary deadline")
		}
		inner, cleanupInner, err := enterInvocationContext(detached, testInvocationID(t, "77777777777777777777777777777777"), time.Hour)
		if err != nil {
			t.Fatalf("enter inner: %v", err)
		}
		defer cleanupInner()
		deadline, exists := inner.Deadline()
		if !exists || !deadline.Equal(authorityDeadline) {
			t.Fatalf("nested frame deadline = %v, want %v", deadline, authorityDeadline)
		}
	})
}

func TestEnterInvocationContextPreservesNestedCancellationAuthority(t *testing.T) {
	t.Parallel()

	authority, cancelAuthority := context.WithCancel(context.Background())
	outer, cleanupOuter, err := enterInvocationContext(authority, testInvocationID(t, "88888888888888888888888888888888"), time.Minute)
	if err != nil {
		cancelAuthority()
		t.Fatalf("enter outer: %v", err)
	}
	defer cleanupOuter()
	inner, cleanupInner, err := enterInvocationContext(context.WithoutCancel(outer), testInvocationID(t, "99999999999999999999999999999999"), time.Minute)
	if err != nil {
		cancelAuthority()
		t.Fatalf("enter inner: %v", err)
	}
	defer cleanupInner()

	cancelAuthority()
	select {
	case <-inner.Done():
	case <-time.After(time.Second):
		t.Fatal("root cancellation did not reach detached nested context")
	}
	if !errors.Is(inner.Err(), context.Canceled) {
		t.Fatalf("inner error = %v", inner.Err())
	}
	cleanupInner()
	cleanupInner()
}

func TestInvocationContextAccessorsFailClosed(t *testing.T) {
	t.Parallel()

	contexts := []context.Context{
		nil,
		context.Background(),
		context.WithValue(context.Background(), runtimeFrameKey{}, runtimeFrame{}),
		context.WithValue(context.Background(), runtimeFrameKey{}, "forged"),
	}
	for _, ctx := range contexts {
		if requestID, exists := RequestID(ctx); exists || requestID.Valid() {
			t.Fatalf("RequestID(%#v) = %q, %t", ctx, requestID, exists)
		}
		if traceID, exists := TraceID(ctx); exists || traceID.Valid() {
			t.Fatalf("TraceID(%#v) = %q, %t", ctx, traceID, exists)
		}
		if frame, exists := runtimeFrameFrom(ctx); exists || frame.valid() {
			t.Fatalf("runtimeFrameFrom(%#v) = %#v, %t", ctx, frame, exists)
		}
		if current, exists := Current(ctx); exists || current.RequestID().Valid() || current.TraceID().Valid() ||
			current.InvocationID().Valid() || current.ParentInvocationID().Valid() || !current.Deadline().IsZero() {
			t.Fatalf("Current(%#v) = %#v, %t", ctx, current, exists)
		}
	}
}

func TestEnterInvocationContextRejectsInvalidInputs(t *testing.T) {
	t.Parallel()

	outerID := testInvocationID(t, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	innerID := testInvocationID(t, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	outer, cleanupOuter, err := enterInvocationContext(context.Background(), outerID, time.Minute)
	if err != nil {
		t.Fatalf("enter outer: %v", err)
	}
	defer cleanupOuter()
	inner, cleanupInner, err := enterInvocationContext(outer, innerID, time.Minute)
	if err != nil {
		t.Fatalf("enter inner: %v", err)
	}
	defer cleanupInner()
	corruptFrame, _ := runtimeFrameFrom(outer)
	corruptFrame.requestID = audit.RequestID{}
	corrupt := context.WithValue(outer, runtimeFrameKey{}, corruptFrame)
	forged := context.WithValue(context.Background(), runtimeFrameKey{}, "forged")

	for _, test := range []struct {
		name       string
		parent     context.Context
		invocation audit.InvocationID
		timeout    time.Duration
	}{
		{name: "nil parent", invocation: outerID, timeout: time.Minute},
		{name: "zero invocation", parent: context.Background(), timeout: time.Minute},
		{name: "zero timeout", parent: context.Background(), invocation: outerID},
		{name: "negative timeout", parent: context.Background(), invocation: outerID, timeout: -time.Second},
		{name: "corrupt frame", parent: corrupt, invocation: innerID, timeout: time.Minute},
		{name: "forged frame", parent: forged, invocation: innerID, timeout: time.Minute},
		{name: "duplicate invocation", parent: outer, invocation: outerID, timeout: time.Minute},
		{name: "duplicate parent invocation", parent: inner, invocation: outerID, timeout: time.Minute},
	} {
		created, createdCleanup, err := enterInvocationContext(test.parent, test.invocation, test.timeout)
		if !errors.Is(err, ErrInvalidInvocationContext) || created != nil || createdCleanup != nil {
			t.Fatalf("%s enterInvocationContext = %#v, cleanup present %t, %v", test.name, created, createdCleanup != nil, err)
		}
	}
}

func testInvocationID(t *testing.T, value string) audit.InvocationID {
	t.Helper()
	id, err := audit.ParseInvocationID(value)
	if err != nil {
		t.Fatalf("ParseInvocationID(%q): %v", value, err)
	}
	return id
}
