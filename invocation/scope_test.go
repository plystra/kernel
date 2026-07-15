package invocation

import (
	"errors"
	"testing"

	"github.com/plystra/kernel/audit"
	"github.com/plystra/kernel/plugin"
)

func TestDispatcherScopesValidatedCallersBeforePublication(t *testing.T) {
	t.Parallel()

	dispatcher := newTestDispatcher(t)
	pluginID, err := plugin.ParseID("acme.scope.caller")
	if err != nil {
		t.Fatalf("ParseID: %v", err)
	}
	pluginCaller, err := audit.NewPluginCallerIdentity(pluginID)
	if err != nil {
		t.Fatalf("NewPluginCallerIdentity: %v", err)
	}

	for _, caller := range []audit.CallerIdentity{
		audit.NewKernelCallerIdentity(),
		pluginCaller,
	} {
		scope, err := dispatcher.Scope(caller)
		if err != nil {
			t.Fatalf("Scope(%s): %v", caller.Kind(), err)
		}
		if !scope.valid() || scope.dispatcher != dispatcher || scope.caller != caller {
			t.Fatalf("Scope(%s) = %#v", caller.Kind(), scope)
		}
	}
	if dispatcher.Published() {
		t.Fatal("creating a scope published the Dispatcher")
	}
}

func TestDispatcherScopeRemainsBoundAfterPublication(t *testing.T) {
	t.Parallel()

	dispatcher := newTestDispatcher(t)
	caller := audit.NewKernelCallerIdentity()
	scope, err := dispatcher.Scope(caller)
	if err != nil {
		t.Fatalf("Scope: %v", err)
	}
	catalog, err := NewCatalog(nil)
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	if err := dispatcher.Publish(catalog); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if !scope.valid() || scope.dispatcher != dispatcher || scope.caller != caller {
		t.Fatalf("published Scope = %#v", scope)
	}
}

func TestDispatcherScopeRejectsInvalidInputs(t *testing.T) {
	t.Parallel()

	caller := audit.NewKernelCallerIdentity()
	var nilDispatcher *Dispatcher
	if scope, err := nilDispatcher.Scope(caller); !errors.Is(err, ErrInvalidScope) || scope.valid() {
		t.Fatalf("nil Dispatcher Scope = %#v, %v", scope, err)
	}
	var zeroDispatcher Dispatcher
	if scope, err := zeroDispatcher.Scope(caller); !errors.Is(err, ErrInvalidScope) || scope.valid() {
		t.Fatalf("zero Dispatcher Scope = %#v, %v", scope, err)
	}
	dispatcher := newTestDispatcher(t)
	if scope, err := dispatcher.Scope(audit.CallerIdentity{}); !errors.Is(err, ErrInvalidScope) || scope.valid() {
		t.Fatalf("zero caller Scope = %#v, %v", scope, err)
	}
	if (Scope{}).valid() {
		t.Fatal("zero Scope is valid")
	}
}
