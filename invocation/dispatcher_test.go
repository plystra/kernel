package invocation

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/plystra/kernel/audit"
	"github.com/plystra/kernel/capability"
)

func TestNewDispatcherRequiresRuntimeConfiguration(t *testing.T) {
	t.Parallel()

	recorder := newTestInvocationRecorder(t)
	for _, timeout := range []time.Duration{0, -time.Nanosecond, -time.Second} {
		dispatcher, err := NewDispatcher(DispatcherOptions{DefaultTimeout: timeout, AuditRecorder: recorder})
		if !errors.Is(err, ErrInvalidDispatcher) || dispatcher != nil {
			t.Fatalf("NewDispatcher(%s) = %#v, %v", timeout, dispatcher, err)
		}
	}
	if dispatcher, err := NewDispatcher(DispatcherOptions{DefaultTimeout: time.Second}); !errors.Is(err, ErrInvalidDispatcher) || dispatcher != nil {
		t.Fatalf("NewDispatcher(nil audit recorder) = %#v, %v", dispatcher, err)
	}
	dispatcher, err := NewDispatcher(DispatcherOptions{DefaultTimeout: 30 * time.Second, AuditRecorder: recorder})
	if err != nil || !dispatcher.valid() || dispatcher.defaultTimeout != 30*time.Second || dispatcher.auditRecorder != recorder || dispatcher.Published() {
		t.Fatalf("NewDispatcher(valid) = %#v, %v", dispatcher, err)
	}
}

func TestDispatcherPublishesImmutableCatalogOnce(t *testing.T) {
	t.Parallel()

	dispatcher := newTestDispatcher(t)
	if state, err := dispatcher.snapshot(); !errors.Is(err, ErrDispatcherNotReady) || state != nil {
		t.Fatalf("unpublished snapshot = %#v, %v", state, err)
	}
	catalog, identifier := testDispatcherCatalog(t, "example.dispatch/v1")
	if err := dispatcher.Publish(catalog); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if !dispatcher.Published() {
		t.Fatal("Dispatcher did not report the published catalog")
	}

	delete(catalog.state.entries, identifier)
	catalog.state.ordered[0] = Binding{}
	state, err := dispatcher.snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	binding, exists := state.entries[identifier]
	if !exists || binding.Capability() != identifier || len(state.ordered) != 1 || state.ordered[0].Capability() != identifier {
		t.Fatalf("published state changed with source Catalog: %#v", state)
	}
	if err := dispatcher.Publish(catalog); !errors.Is(err, ErrCatalogPublished) {
		t.Fatalf("second Publish error = %v, want ErrCatalogPublished", err)
	}
}

func TestDispatcherPublishesEmptyCatalogDistinctFromUnpublished(t *testing.T) {
	t.Parallel()

	dispatcher := newTestDispatcher(t)
	catalog, err := NewCatalog(nil)
	if err != nil {
		t.Fatalf("NewCatalog(nil): %v", err)
	}
	if err := dispatcher.Publish(catalog); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	state, err := dispatcher.snapshot()
	if err != nil || state == nil || state.entries == nil || state.ordered == nil || len(state.entries) != 0 || len(state.ordered) != 0 {
		t.Fatalf("empty published snapshot = %#v, %v", state, err)
	}
}

func TestDispatcherRejectsInvalidPublicationWithoutChangingState(t *testing.T) {
	t.Parallel()

	dispatcher := newTestDispatcher(t)
	if err := dispatcher.Publish(Catalog{}); !errors.Is(err, ErrInvalidCatalog) {
		t.Fatalf("zero Publish error = %v, want ErrInvalidCatalog", err)
	}
	if state, err := dispatcher.snapshot(); !errors.Is(err, ErrDispatcherNotReady) || state != nil {
		t.Fatalf("snapshot after failed Publish = %#v, %v", state, err)
	}
	valid, err := NewCatalog(nil)
	if err != nil {
		t.Fatalf("NewCatalog(nil): %v", err)
	}
	if err := dispatcher.Publish(valid); err != nil {
		t.Fatalf("valid Publish after rejection: %v", err)
	}

	var nilDispatcher *Dispatcher
	if nilDispatcher.Published() {
		t.Fatal("nil Dispatcher reports a published catalog")
	}
	if err := nilDispatcher.Publish(valid); !errors.Is(err, ErrInvalidDispatcher) {
		t.Fatalf("nil Dispatcher Publish error = %v", err)
	}
	if state, err := nilDispatcher.snapshot(); !errors.Is(err, ErrInvalidDispatcher) || state != nil {
		t.Fatalf("nil Dispatcher snapshot = %#v, %v", state, err)
	}
	var zero Dispatcher
	if zero.Published() {
		t.Fatal("zero Dispatcher reports a published catalog")
	}
	if err := zero.Publish(valid); !errors.Is(err, ErrInvalidDispatcher) {
		t.Fatalf("zero Dispatcher Publish error = %v", err)
	}
}

func TestDispatcherConcurrentPublicationHasOneCompleteWinner(t *testing.T) {
	dispatcher := newTestDispatcher(t)
	catalogA, identifierA := testDispatcherCatalog(t, "example.concurrent-a/v1")
	catalogB, identifierB := testDispatcherCatalog(t, "example.concurrent-b/v1")
	catalogs := []Catalog{catalogA, catalogB}

	const publishers = 64
	var successes atomic.Int32
	var alreadyPublished atomic.Int32
	var group sync.WaitGroup
	group.Add(publishers)
	for index := range publishers {
		go func(catalog Catalog) {
			defer group.Done()
			switch err := dispatcher.Publish(catalog); {
			case err == nil:
				successes.Add(1)
			case errors.Is(err, ErrCatalogPublished):
				alreadyPublished.Add(1)
			default:
				t.Errorf("Publish error = %v", err)
			}
		}(catalogs[index%len(catalogs)])
	}
	group.Wait()
	if successes.Load() != 1 || alreadyPublished.Load() != publishers-1 {
		t.Fatalf("publication results = %d success, %d already", successes.Load(), alreadyPublished.Load())
	}
	state, err := dispatcher.snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	_, hasA := state.entries[identifierA]
	_, hasB := state.entries[identifierB]
	if hasA == hasB || len(state.entries) != 1 || len(state.ordered) != 1 {
		t.Fatalf("winner is not one complete catalog: %#v", state)
	}
}

func TestDispatcherSnapshotsAreSafeDuringPublicationAndAllocateNothing(t *testing.T) {
	dispatcher := newTestDispatcher(t)
	catalog, identifier := testDispatcherCatalog(t, "example.snapshot/v1")

	const readers = 64
	start := make(chan struct{})
	var group sync.WaitGroup
	group.Add(readers)
	for range readers {
		go func() {
			defer group.Done()
			<-start
			state, err := dispatcher.snapshot()
			if errors.Is(err, ErrDispatcherNotReady) {
				return
			}
			if err != nil {
				t.Errorf("snapshot error = %v", err)
				return
			}
			if _, exists := state.entries[identifier]; !exists || len(state.ordered) != 1 {
				t.Errorf("published snapshot is incomplete: %#v", state)
			}
		}()
	}
	close(start)
	if err := dispatcher.Publish(catalog); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	group.Wait()

	var state *catalogState
	allocations := testing.AllocsPerRun(1000, func() {
		var err error
		state, err = dispatcher.snapshot()
		if err != nil {
			panic(err)
		}
	})
	if allocations != 0 || state == nil || len(state.entries) != 1 {
		t.Fatalf("snapshot allocations = %f, state %#v", allocations, state)
	}
}

func newTestDispatcher(t *testing.T) *Dispatcher {
	t.Helper()
	dispatcher, err := NewDispatcher(DispatcherOptions{
		DefaultTimeout: time.Second,
		AuditRecorder:  newTestInvocationRecorder(t),
	})
	if err != nil {
		t.Fatalf("NewDispatcher: %v", err)
	}
	return dispatcher
}

func newTestInvocationRecorder(t *testing.T) *audit.InvocationRecorder {
	t.Helper()
	recorder, err := audit.NewInvocationRecorder(testInvocationAuditSink{}, time.Second)
	if err != nil {
		t.Fatalf("NewInvocationRecorder: %v", err)
	}
	return recorder
}

type testInvocationAuditSink struct{}

func (testInvocationAuditSink) PersistInvocation(context.Context, audit.InvocationRecord) error {
	return nil
}

func (testInvocationAuditSink) Flush(context.Context) error { return nil }

func testDispatcherCatalog(t *testing.T, value string) (Catalog, capability.Identifier) {
	t.Helper()
	providerID := mustPluginID(t, "acme.dispatch.provider")
	binding := testBinding(t, value, providerID)
	catalog, err := NewCatalog([]Binding{binding})
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	return catalog, binding.Capability()
}
