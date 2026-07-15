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
	}
}
