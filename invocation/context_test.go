package invocation

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/plystra/kernel/audit"
	"github.com/plystra/kernel/plugin"
)

type rootContextValueKey struct{}

func TestKernelScopeMintsRootContext(t *testing.T) {
	t.Parallel()

	scope := testHandleScope(t)
	subject, err := audit.NewSubjectContext("subject:01", "tenant:west")
	if err != nil {
		t.Fatalf("NewSubjectContext: %v", err)
	}
	parent := context.WithValue(context.Background(), rootContextValueKey{}, "preserved")
	root, err := scope.NewRootContext(parent, subject)
	if err != nil {
		t.Fatalf("NewRootContext: %v", err)
	}
	if root.Value(rootContextValueKey{}) != "preserved" {
		t.Fatal("root context lost an ordinary parent value")
	}
	frame, exists := runtimeFrameFrom(root)
	if !exists || !frame.validRoot() {
		t.Fatalf("root frame = %#v, exists %t", frame, exists)
	}
	if !frame.requestID.Valid() || !frame.traceID.Valid() || frame.requestID.String() == frame.traceID.String() {
		t.Fatalf("root identities = %q / %q", frame.requestID, frame.traceID)
	}
	if frame.invocationID.Valid() || frame.parentID.Valid() || frame.subject != subject || frame.authority != parent || !frame.deadline.IsZero() {
		t.Fatalf("root frame fields = %#v", frame)
	}
	requestID, requestExists := RequestID(root)
	traceID, traceExists := TraceID(root)
	if !requestExists || !traceExists || requestID != frame.requestID || traceID != frame.traceID {
		t.Fatalf("public IDs = %q/%t, %q/%t", requestID, requestExists, traceID, traceExists)
	}
	if _, exists := runtimeFrameFrom(parent); exists {
		t.Fatal("NewRootContext mutated its parent")
	}
	if scope.dispatcher.Published() {
		t.Fatal("NewRootContext published the Dispatcher")
	}
}

func TestRootContextPreservesTrustedDeadlineAndCancellationAuthority(t *testing.T) {
	t.Parallel()

	scope := testHandleScope(t)
	subject, err := audit.NewSubjectContext("", "")
	if err != nil {
		t.Fatalf("NewSubjectContext: %v", err)
	}
	deadline := time.Now().Add(time.Minute)
	authority, cancelDeadline := context.WithDeadline(context.Background(), deadline)
	root, err := scope.NewRootContext(authority, subject)
	if err != nil {
		cancelDeadline()
		t.Fatalf("NewRootContext: %v", err)
	}
	detached := context.WithoutCancel(root)
	if _, exists := detached.Deadline(); exists || detached.Err() != nil {
		cancelDeadline()
		t.Fatal("test context did not detach ordinary cancellation")
	}
	frame, exists := runtimeFrameFrom(detached)
	if !exists || !frame.deadline.Equal(deadline) || frame.authority != authority {
		cancelDeadline()
		t.Fatalf("detached frame = %#v, exists %t", frame, exists)
	}
	cancelDeadline()
	if !errors.Is(frame.authority.Err(), context.Canceled) {
		t.Fatalf("trusted authority error = %v", frame.authority.Err())
	}
	if detached.Err() != nil {
		t.Fatalf("ordinary detached context unexpectedly cancelled: %v", detached.Err())
	}
}

func TestRootContextRejectsUntrustedInputsAndReplacement(t *testing.T) {
	t.Parallel()

	subject, err := audit.NewSubjectContext("", "")
	if err != nil {
		t.Fatalf("NewSubjectContext: %v", err)
	}
	kernelScope := testHandleScope(t)
	dispatcher := newTestDispatcher(t)
	pluginID, err := plugin.ParseID("acme.context.caller")
	if err != nil {
		t.Fatalf("ParseID: %v", err)
	}
	pluginCaller, err := audit.NewPluginCallerIdentity(pluginID)
	if err != nil {
		t.Fatalf("NewPluginCallerIdentity: %v", err)
	}
	pluginScope, err := dispatcher.Scope(pluginCaller)
	if err != nil {
		t.Fatalf("Scope: %v", err)
	}
	root, err := kernelScope.NewRootContext(context.Background(), subject)
	if err != nil {
		t.Fatalf("NewRootContext: %v", err)
	}
	corrupt := context.WithValue(context.Background(), runtimeFrameKey{}, runtimeFrame{})
	for _, test := range []struct {
		name    string
		scope   Scope
		parent  context.Context
		subject audit.SubjectContext
	}{
		{name: "zero scope", parent: context.Background(), subject: subject},
		{name: "plugin scope", scope: pluginScope, parent: context.Background(), subject: subject},
		{name: "nil parent", scope: kernelScope, subject: subject},
		{name: "zero subject", scope: kernelScope, parent: context.Background()},
		{name: "nested root", scope: kernelScope, parent: root, subject: subject},
		{name: "corrupt existing frame", scope: kernelScope, parent: corrupt, subject: subject},
	} {
		created, err := test.scope.NewRootContext(test.parent, test.subject)
		if !errors.Is(err, ErrInvalidInvocationContext) || created != nil {
			t.Fatalf("%s NewRootContext = %#v, %v", test.name, created, err)
		}
	}
}

func TestRuntimeContextAccessorsFailClosed(t *testing.T) {
	t.Parallel()

	for _, ctx := range []context.Context{nil, context.Background(), context.WithValue(context.Background(), runtimeFrameKey{}, runtimeFrame{})} {
		if requestID, exists := RequestID(ctx); exists || requestID.Valid() {
			t.Fatalf("RequestID(%#v) = %q, %t", ctx, requestID, exists)
		}
		if traceID, exists := TraceID(ctx); exists || traceID.Valid() {
			t.Fatalf("TraceID(%#v) = %q, %t", ctx, traceID, exists)
		}
		if frame, exists := runtimeFrameFrom(ctx); exists || frame.validRoot() {
			t.Fatalf("runtimeFrameFrom(%#v) = %#v, %t", ctx, frame, exists)
		}
		if current, exists := Current(ctx); exists || current.RequestID().Valid() || current.TraceID().Valid() || current.InvocationID().Valid() || current.ParentInvocationID().Valid() || current.SubjectIdentity() != "" || current.TenantIdentity() != "" || !current.Deadline().IsZero() {
			t.Fatalf("Current(%#v) = %#v, %t", ctx, current, exists)
		}
	}
}

func TestEnterInvocationContextPreservesGovernedAncestry(t *testing.T) {
	t.Parallel()

	scope := testHandleScope(t)
	subject, err := audit.NewSubjectContext("subject:operator", "tenant:west")
	if err != nil {
		t.Fatalf("NewSubjectContext: %v", err)
	}
	root, err := scope.NewRootContext(context.Background(), subject)
	if err != nil {
		t.Fatalf("NewRootContext: %v", err)
	}
	requestID, _ := RequestID(root)
	traceID, _ := TraceID(root)
	outerID := testInvocationID(t, "11111111111111111111111111111111")
	innerID := testInvocationID(t, "22222222222222222222222222222222")

	outer, cleanupOuter, err := enterInvocationContext(root, outerID, time.Minute)
	if err != nil {
		t.Fatalf("enter outer: %v", err)
	}
	defer cleanupOuter()
	outerDeadline, hasOuterDeadline := outer.Deadline()
	outerCurrent, exists := Current(outer)
	if !exists || !hasOuterDeadline {
		t.Fatalf("outer Current = %#v, %t; deadline exists %t", outerCurrent, exists, hasOuterDeadline)
	}
	if outerCurrent.RequestID() != requestID || outerCurrent.TraceID() != traceID || outerCurrent.InvocationID() != outerID || outerCurrent.ParentInvocationID().Valid() {
		t.Fatalf("outer identities = %#v", outerCurrent)
	}
	if outerCurrent.SubjectIdentity() != "subject:operator" || outerCurrent.TenantIdentity() != "tenant:west" || !outerCurrent.Deadline().Equal(outerDeadline) {
		t.Fatalf("outer governed values = %#v", outerCurrent)
	}

	inner, cleanupInner, err := enterInvocationContext(outer, innerID, 2*time.Minute)
	if err != nil {
		t.Fatalf("enter inner: %v", err)
	}
	defer cleanupInner()
	innerCurrent, exists := Current(inner)
	if !exists {
		t.Fatal("inner Current missing")
	}
	if innerCurrent.RequestID() != requestID || innerCurrent.TraceID() != traceID || innerCurrent.InvocationID() != innerID || innerCurrent.ParentInvocationID() != outerID {
		t.Fatalf("inner identities = %#v", innerCurrent)
	}
	if innerCurrent.SubjectIdentity() != outerCurrent.SubjectIdentity() || innerCurrent.TenantIdentity() != outerCurrent.TenantIdentity() || !innerCurrent.Deadline().Equal(outerDeadline) {
		t.Fatalf("inner governed values = %#v", innerCurrent)
	}
	if unchanged, _ := Current(outer); unchanged != outerCurrent {
		t.Fatalf("nested entry mutated outer context: %#v != %#v", unchanged, outerCurrent)
	}
	if rootCurrent, exists := Current(root); exists || rootCurrent.InvocationID().Valid() {
		t.Fatalf("root Current = %#v, %t", rootCurrent, exists)
	}
}

func TestEnterInvocationContextChoosesEarliestDeadline(t *testing.T) {
	t.Parallel()

	scope := testHandleScope(t)
	subject, err := audit.NewSubjectContext("", "")
	if err != nil {
		t.Fatalf("NewSubjectContext: %v", err)
	}

	t.Run("configured default", func(t *testing.T) {
		root, err := scope.NewRootContext(context.Background(), subject)
		if err != nil {
			t.Fatalf("NewRootContext: %v", err)
		}
		const timeout = time.Minute
		earliest := time.Now().Add(timeout)
		entered, cleanup, err := enterInvocationContext(root, testInvocationID(t, "33333333333333333333333333333333"), timeout)
		latest := time.Now().Add(timeout)
		if err != nil {
			t.Fatalf("enterInvocationContext: %v", err)
		}
		defer cleanup()
		deadline, exists := entered.Deadline()
		if !exists || deadline.Before(earliest) || deadline.After(latest) {
			t.Fatalf("configured deadline = %v, want between %v and %v", deadline, earliest, latest)
		}
		current, _ := Current(entered)
		if !current.Deadline().Equal(deadline) {
			t.Fatalf("Current deadline = %v, context deadline %v", current.Deadline(), deadline)
		}
	})

	t.Run("ordinary caller", func(t *testing.T) {
		root, err := scope.NewRootContext(context.Background(), subject)
		if err != nil {
			t.Fatalf("NewRootContext: %v", err)
		}
		callerDeadline := time.Now().Add(time.Minute)
		caller, cancelCaller := context.WithDeadline(root, callerDeadline)
		defer cancelCaller()
		entered, cleanup, err := enterInvocationContext(caller, testInvocationID(t, "44444444444444444444444444444444"), time.Hour)
		if err != nil {
			t.Fatalf("enterInvocationContext: %v", err)
		}
		defer cleanup()
		deadline, exists := entered.Deadline()
		if !exists || !deadline.Equal(callerDeadline) {
			t.Fatalf("ordinary caller deadline = %v, want %v", deadline, callerDeadline)
		}
	})

	t.Run("trusted frame", func(t *testing.T) {
		trustedDeadline := time.Now().Add(time.Minute)
		authority, cancelAuthority := context.WithDeadline(context.Background(), trustedDeadline)
		defer cancelAuthority()
		root, err := scope.NewRootContext(authority, subject)
		if err != nil {
			t.Fatalf("NewRootContext: %v", err)
		}
		detached := context.WithoutCancel(root)
		if _, exists := detached.Deadline(); exists {
			t.Fatal("test context retained ordinary deadline")
		}
		entered, cleanup, err := enterInvocationContext(detached, testInvocationID(t, "55555555555555555555555555555555"), time.Hour)
		if err != nil {
			t.Fatalf("enterInvocationContext: %v", err)
		}
		defer cleanup()
		deadline, exists := entered.Deadline()
		if !exists || !deadline.Equal(trustedDeadline) {
			t.Fatalf("trusted frame deadline = %v, want %v", deadline, trustedDeadline)
		}
	})
}

func TestEnterInvocationContextLinksTrustedCancellationAndCleansUp(t *testing.T) {
	t.Parallel()

	scope := testHandleScope(t)
	subject, err := audit.NewSubjectContext("", "")
	if err != nil {
		t.Fatalf("NewSubjectContext: %v", err)
	}

	t.Run("detached cancellation", func(t *testing.T) {
		authority, cancelAuthority := context.WithCancel(context.Background())
		root, err := scope.NewRootContext(authority, subject)
		if err != nil {
			cancelAuthority()
			t.Fatalf("NewRootContext: %v", err)
		}
		entered, cleanup, err := enterInvocationContext(context.WithoutCancel(root), testInvocationID(t, "66666666666666666666666666666666"), time.Minute)
		if err != nil {
			cancelAuthority()
			t.Fatalf("enterInvocationContext: %v", err)
		}
		cancelAuthority()
		select {
		case <-entered.Done():
		case <-time.After(time.Second):
			t.Fatal("trusted cancellation did not reach entered context")
		}
		if !errors.Is(entered.Err(), context.Canceled) {
			t.Fatalf("entered error = %v", entered.Err())
		}
		cleanup()
		cleanup()
	})

	t.Run("already cancelled authority", func(t *testing.T) {
		authority, cancelAuthority := context.WithCancel(context.Background())
		root, err := scope.NewRootContext(authority, subject)
		if err != nil {
			cancelAuthority()
			t.Fatalf("NewRootContext: %v", err)
		}
		cancelAuthority()
		entered, cleanup, err := enterInvocationContext(context.WithoutCancel(root), testInvocationID(t, "77777777777777777777777777777777"), time.Minute)
		if err != nil {
			t.Fatalf("enterInvocationContext: %v", err)
		}
		defer cleanup()
		select {
		case <-entered.Done():
		case <-time.After(time.Second):
			t.Fatal("already-cancelled authority did not cancel entered context")
		}
		if !errors.Is(entered.Err(), context.Canceled) {
			t.Fatalf("entered error = %v", entered.Err())
		}
	})

	t.Run("cleanup", func(t *testing.T) {
		root, err := scope.NewRootContext(context.Background(), subject)
		if err != nil {
			t.Fatalf("NewRootContext: %v", err)
		}
		entered, cleanup, err := enterInvocationContext(root, testInvocationID(t, "88888888888888888888888888888888"), time.Minute)
		if err != nil {
			t.Fatalf("enterInvocationContext: %v", err)
		}
		cleanup()
		cleanup()
		if !errors.Is(entered.Err(), context.Canceled) {
			t.Fatalf("cleanup error = %v", entered.Err())
		}
	})
}

func TestEnterInvocationContextRejectsInvalidInputs(t *testing.T) {
	t.Parallel()

	scope := testHandleScope(t)
	subject, err := audit.NewSubjectContext("", "")
	if err != nil {
		t.Fatalf("NewSubjectContext: %v", err)
	}
	root, err := scope.NewRootContext(context.Background(), subject)
	if err != nil {
		t.Fatalf("NewRootContext: %v", err)
	}
	invocationID := testInvocationID(t, "99999999999999999999999999999999")
	entered, cleanup, err := enterInvocationContext(root, invocationID, time.Minute)
	if err != nil {
		t.Fatalf("valid enterInvocationContext: %v", err)
	}
	defer cleanup()
	corruptFrame, _ := runtimeFrameFrom(root)
	corruptFrame.requestID = audit.RequestID{}
	corrupt := context.WithValue(root, runtimeFrameKey{}, corruptFrame)

	for _, test := range []struct {
		name       string
		parent     context.Context
		invocation audit.InvocationID
		timeout    time.Duration
	}{
		{name: "nil parent", invocation: invocationID, timeout: time.Minute},
		{name: "missing frame", parent: context.Background(), invocation: invocationID, timeout: time.Minute},
		{name: "corrupt frame", parent: corrupt, invocation: invocationID, timeout: time.Minute},
		{name: "zero invocation", parent: root, timeout: time.Minute},
		{name: "duplicate invocation", parent: entered, invocation: invocationID, timeout: time.Minute},
		{name: "zero timeout", parent: root, invocation: invocationID},
		{name: "negative timeout", parent: root, invocation: invocationID, timeout: -time.Second},
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
