package invocation

import (
	"errors"
	"sync"
	"sync/atomic"

	"github.com/plystra/kernel/capability"
	"go.opentelemetry.io/otel/metric"
)

var (
	// ErrInvalidDispatcher reports a nil or improperly configured Dispatcher.
	ErrInvalidDispatcher = errors.New("invalid capability dispatcher")
	// ErrCatalogPublished reports an attempt to replace the live immutable
	// catalog.
	ErrCatalogPublished = errors.New("capability endpoint catalog already published")
	// ErrDispatcherNotReady reports an unpublished catalog or unopened admission.
	ErrDispatcherNotReady = errors.New("capability dispatcher is not ready")
	// ErrDispatcherDraining reports permanent admission closure during shutdown.
	ErrDispatcherDraining = errors.New("capability dispatcher admission is closed")
)

// DispatcherOptions configures mandatory runtime dispatch behavior.
type DispatcherOptions struct {
	PolicyVersion int
	// MeterProvider receives intrinsic lifetime metrics. Nil uses the global
	// OpenTelemetry provider; the Kernel does not configure exporters or own it.
	MeterProvider metric.MeterProvider
}

// Dispatcher owns one atomically published immutable executable catalog. A
// Dispatcher must not be copied after first use.
type Dispatcher struct {
	policyVersion int
	catalog       atomic.Pointer[catalogState]
	mu            sync.Mutex
	attempts      map[*targetAttempt]struct{}
	inflight      map[capability.Identifier]int
	admissionOpen bool
	draining      bool
	drained       chan struct{}
	closing       chan struct{}
	metrics       invocationMetrics
}

// NewDispatcher creates an unpublished Dispatcher for the exact compiled-policy
// protocol with admission closed until OpenAdmission. Deadlines are resolved
// per binding, never supplied as a fallback.
func NewDispatcher(options DispatcherOptions) (*Dispatcher, error) {
	if options.PolicyVersion != PolicySchemaVersion {
		return nil, ErrInvalidDispatcher
	}
	metrics, err := newInvocationMetrics(options.MeterProvider)
	if err != nil {
		return nil, err
	}
	return &Dispatcher{
		policyVersion: options.PolicyVersion,
		attempts:      make(map[*targetAttempt]struct{}),
		inflight:      make(map[capability.Identifier]int),
		drained:       make(chan struct{}),
		closing:       make(chan struct{}),
		metrics:       metrics,
	}, nil
}

// Publish atomically installs one complete catalog exactly once. The catalog
// state is copied before publication so the live snapshot has no mutable source
// alias. Publication does not open admission or assert lifecycle readiness.
func (d *Dispatcher) Publish(catalog Catalog) error {
	if !d.valid() {
		return ErrInvalidDispatcher
	}
	if !catalog.valid() {
		return ErrInvalidCatalog
	}
	state := cloneCatalogState(catalog.state)
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.draining {
		return ErrDispatcherDraining
	}
	if !d.catalog.CompareAndSwap(nil, state) {
		return ErrCatalogPublished
	}
	return nil
}

// Published reports whether one complete executable catalog was published.
// Publication is immutable and does not imply admission is open.
func (d *Dispatcher) Published() bool {
	return d.valid() && d.catalog.Load() != nil
}

// OpenAdmission permits invocation of the published catalog. Generated assembly
// must call it only after all selected values are ready and transports are bound.
// The dispatcher does not discover or start lifecycle values. Repeated calls are
// idempotent until Drain permanently closes admission; they cannot reopen it.
func (d *Dispatcher) OpenAdmission() error {
	if !d.valid() {
		return ErrInvalidDispatcher
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.draining {
		return ErrDispatcherDraining
	}
	if d.catalog.Load() == nil {
		return ErrDispatcherNotReady
	}
	d.admissionOpen = true
	return nil
}

// Accepting reports whether admission has opened and shutdown has not begun.
// It is an observation, not a reservation: a concurrent Drain can still reject
// a subsequent call. Published and AdmissionClosed remain separate facts.
func (d *Dispatcher) Accepting() bool {
	if !d.valid() {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.admissionOpen && !d.draining
}

func (d *Dispatcher) admissionBoundary() *Error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.admissionBoundaryLocked()
}

func (d *Dispatcher) admissionBoundaryLocked() *Error {
	if d.draining {
		return newNotStartedBoundary(ErrorUnavailable, detailDispatcherDraining)
	}
	if !d.admissionOpen {
		return newNotStartedBoundary(ErrorUnavailable, detailDispatcherNotReady)
	}
	return nil
}

func (d *Dispatcher) valid() bool {
	return d != nil && d.policyVersion == PolicySchemaVersion && d.closing != nil
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
