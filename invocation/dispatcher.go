package invocation

import (
	"errors"
	"sync/atomic"
	"time"

	"github.com/plystra/kernel/audit"
	"github.com/plystra/kernel/capability"
)

var (
	// ErrInvalidDispatcher reports a nil or improperly configured Dispatcher.
	ErrInvalidDispatcher = errors.New("invalid capability dispatcher")
	// ErrCatalogPublished reports an attempt to replace the live immutable
	// catalog.
	ErrCatalogPublished = errors.New("capability endpoint catalog already published")
	// ErrDispatcherNotReady reports that no complete catalog is live yet.
	ErrDispatcherNotReady = errors.New("capability dispatcher is not ready")
)

// DispatcherOptions configures mandatory runtime dispatch behavior.
type DispatcherOptions struct {
	DefaultTimeout time.Duration
	AuditRecorder  *audit.InvocationRecorder
}

// Dispatcher owns one atomically published immutable executable catalog. A
// Dispatcher must not be copied after first use.
type Dispatcher struct {
	defaultTimeout time.Duration
	auditRecorder  *audit.InvocationRecorder
	catalog        atomic.Pointer[catalogState]
}

// NewDispatcher creates an unpublished Dispatcher with mandatory audit
// recording and a positive default execution timeout.
func NewDispatcher(options DispatcherOptions) (*Dispatcher, error) {
	if options.DefaultTimeout <= 0 || !options.AuditRecorder.Valid() {
		return nil, ErrInvalidDispatcher
	}
	return &Dispatcher{
		defaultTimeout: options.DefaultTimeout,
		auditRecorder:  options.AuditRecorder,
	}, nil
}

// Publish atomically installs one complete catalog exactly once. The catalog
// state is copied before publication so the live snapshot has no mutable source
// alias.
func (d *Dispatcher) Publish(catalog Catalog) error {
	if !d.valid() {
		return ErrInvalidDispatcher
	}
	if !catalog.valid() {
		return ErrInvalidCatalog
	}
	state := cloneCatalogState(catalog.state)
	if !d.catalog.CompareAndSwap(nil, state) {
		return ErrCatalogPublished
	}
	return nil
}

// Published reports whether one complete executable catalog is live.
func (d *Dispatcher) Published() bool {
	return d.valid() && d.catalog.Load() != nil
}

func (d *Dispatcher) valid() bool {
	return d != nil && d.defaultTimeout > 0 && d.auditRecorder.Valid()
}

func (d *Dispatcher) snapshot() (*catalogState, error) {
	if !d.valid() {
		return nil, ErrInvalidDispatcher
	}
	state := d.catalog.Load()
	if state == nil {
		return nil, ErrDispatcherNotReady
	}
	return state, nil
}

func cloneCatalogState(source *catalogState) *catalogState {
	entries := make(map[capability.Identifier]Binding, len(source.entries))
	for identifier, binding := range source.entries {
		entries[identifier] = binding
	}
	ordered := make([]Binding, len(source.ordered))
	copy(ordered, source.ordered)
	return &catalogState{entries: entries, ordered: ordered}
}
