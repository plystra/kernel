package invocation

import (
	"context"
	"errors"
	"strings"
)

var (
	// ErrInvalidLifecycle reports invalid or repeated lifecycle assembly.
	ErrInvalidLifecycle = errors.New("invalid invocation lifecycle assembly")
	// ErrLifecycleState reports a hook attempted outside its lifecycle phase.
	ErrLifecycleState = errors.New("invalid invocation lifecycle state")
	// ErrLifecycleHook reports a redacted hook failure or panic.
	ErrLifecycleHook = errors.New("invocation lifecycle hook failed")
	// ErrInvalidLifecycleOwner reports an invalid typed lifecycle identity.
	ErrInvalidLifecycleOwner = errors.New("invalid invocation lifecycle owner")
)

// LifecycleOwnerKind distinguishes lifecycle identity without extending the
// executable catalog's BindingKind. Resources are never invocation endpoints.
type LifecycleOwnerKind string

const (
	LifecycleOwnerImplementation LifecycleOwnerKind = "implementation"
	LifecycleOwnerResource       LifecycleOwnerKind = "resource"
)

// Valid reports whether the kind identifies a supported lifecycle owner.
func (k LifecycleOwnerKind) Valid() bool {
	return k == LifecycleOwnerImplementation || k == LifecycleOwnerResource
}

// String returns the stable kind representation, or empty for an invalid kind.
func (k LifecycleOwnerKind) String() string {
	if !k.Valid() {
		return ""
	}
	return string(k)
}

// LifecycleOwner is an immutable, comparable identity: an Implementation's
// exact constructor or a Resource's exact configured instance name. Resource
// provider provenance is deliberately not part of identity.
type LifecycleOwner struct {
	kind LifecycleOwnerKind
	name string
}

// NewImplementationOwner validates an exact exported constructor symbol.
func NewImplementationOwner(constructor string) (LifecycleOwner, error) {
	owner := LifecycleOwner{kind: LifecycleOwnerImplementation, name: constructor}
	if !owner.Valid() {
		return LifecycleOwner{}, ErrInvalidLifecycleOwner
	}
	return owner, nil
}

// NewResourceOwner validates an exact configured instance name of 1 through
// 128 ASCII bytes, with dot-separated lower-kebab segments. It does not create
// a constructor symbol, catalog binding, or governed Resource proxy.
func NewResourceOwner(instanceName string) (LifecycleOwner, error) {
	owner := LifecycleOwner{kind: LifecycleOwnerResource, name: instanceName}
	if !owner.Valid() {
		return LifecycleOwner{}, ErrInvalidLifecycleOwner
	}
	return owner, nil
}

// Valid reports whether this is a validated typed identity.
func (o LifecycleOwner) Valid() bool {
	switch o.kind {
	case LifecycleOwnerImplementation:
		separator := strings.LastIndexByte(o.name, '.')
		return separator > 0 && validConstructorSymbol(o.name[:separator], o.name)
	case LifecycleOwnerResource:
		return validResourceInstanceName(o.name)
	default:
		return false
	}
}

// Kind returns the owner's kind, or empty for an invalid owner.
func (o LifecycleOwner) Kind() LifecycleOwnerKind {
	if !o.Valid() {
		return ""
	}
	return o.kind
}

// Constructor returns an Implementation's constructor, or empty for a Resource.
func (o LifecycleOwner) Constructor() string {
	if o.Kind() != LifecycleOwnerImplementation {
		return ""
	}
	return o.name
}

// ResourceInstanceName returns a Resource's exact name, or empty otherwise.
func (o LifecycleOwner) ResourceInstanceName() string {
	if o.Kind() != LifecycleOwnerResource {
		return ""
	}
	return o.name
}

// String returns a safe kind-qualified identity, or empty for an invalid owner.
func (o LifecycleOwner) String() string {
	if !o.Valid() {
		return ""
	}
	return string(o.kind) + " " + o.name
}

func validResourceInstanceName(name string) bool {
	if len(name) == 0 || len(name) > 128 {
		return false
	}
	for index := 0; index < len(name); index++ {
		char := name[index]
		letter := char >= 'a' && char <= 'z'
		digit := char >= '0' && char <= '9'
		if index == 0 || name[index-1] == '.' {
			if !letter {
				return false
			}
		} else if name[index-1] == '-' {
			if !letter && !digit {
				return false
			}
		} else if !letter && !digit && char != '-' && char != '.' {
			return false
		}
	}
	return name[len(name)-1] != '-' && name[len(name)-1] != '.'
}

// Lifecycle coordinates one dispatcher's already-constructed lifecycle values.
// Assembly supplies the complete dependency-first typed owner order before
// publication. Unlisted bindings have no lifecycle work and are ready after
// construction. The lifecycle manager normally owns this assembly API.
type Lifecycle struct {
	dispatcher *Dispatcher
	order      []LifecycleOwner
	owners     map[LifecycleOwner]struct{}
	// Only Implementation constructors participate in Interface readiness.
	ready    map[string]bool
	next     int
	stopping bool
}

type hookScopeKey struct{}

// Mutable lifecycle and scope state is protected by the dispatcher mutex.
type hookScope struct {
	dispatcher *Dispatcher
	context    context.Context
	cancel     context.CancelFunc
	active     bool
	returned   bool
	running    bool
}

// BindLifecycle installs one complete lifecycle order before publication.
// Implementation identity is shared by all Interface bindings selecting that
// constructor. Resource owners never contribute Interface readiness entries.
func (d *Dispatcher) BindLifecycle(owners []LifecycleOwner) (*Lifecycle, error) {
	if !d.valid() {
		return nil, ErrInvalidDispatcher
	}
	ready := make(map[string]bool)
	members := make(map[LifecycleOwner]struct{}, len(owners))
	for _, owner := range owners {
		if !owner.Valid() {
			return nil, ErrInvalidLifecycle
		}
		if _, duplicate := members[owner]; duplicate {
			return nil, ErrInvalidLifecycle
		}
		members[owner] = struct{}{}
		if owner.kind == LifecycleOwnerImplementation {
			ready[owner.name] = false
		}
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.lifecycle != nil || d.catalog.Load() != nil || d.draining {
		return nil, ErrInvalidLifecycle
	}
	lifecycle := &Lifecycle{dispatcher: d, order: append([]LifecycleOwner(nil), owners...), owners: members, ready: ready}
	d.lifecycle = lifecycle
	return lifecycle, nil
}

// Start invokes one owner's Start hook in the supplied order. Only ready
// dependencies are callable through its context; public admission stays closed.
// Success requires the hook and all its target work to finish within ctx.
func (l *Lifecycle) Start(ctx context.Context, owner LifecycleOwner, hook func(context.Context) error) error {
	return l.run(ctx, owner, hook, true)
}

// Stop invokes cleanup after Drain, with access only to dependencies that have
// not begun stopping. It supports never-started values and repeated failed
// cleanup. The lifecycle manager owns reverse ordering and successful-stop
// retention. A nil hook result remains successful after cancellation only if
// all its target work has terminated.
func (l *Lifecycle) Stop(ctx context.Context, owner LifecycleOwner, hook func(context.Context) error) error {
	return l.run(ctx, owner, hook, false)
}

// Drain closes public admission and drains hooks and target attempts before
// cleanup. A failed wait must leave every pending dependency live.
func (l *Lifecycle) Drain(ctx context.Context) error {
	if l == nil || !l.dispatcher.valid() {
		return ErrInvalidLifecycle
	}
	return l.dispatcher.Drain(ctx)
}

func (l *Lifecycle) run(ctx context.Context, owner LifecycleOwner, hook func(context.Context) error, starting bool) (err error) {
	if l == nil || !l.dispatcher.valid() {
		return ErrInvalidLifecycle
	}
	if ctx == nil || hook == nil || !owner.Valid() {
		return ErrInvalidLifecycle
	}
	if _, bounded := ctx.Deadline(); !bounded {
		return ErrInvalidDrainContext
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	d := l.dispatcher
	d.mu.Lock()
	_, member := l.owners[owner]
	if d.lifecycle != l || !member || d.scope != nil || len(d.attempts) != 0 ||
		(starting && (l.stopping || d.draining || d.admissionOpen || d.catalog.Load() == nil || l.next >= len(l.order) || l.order[l.next] != owner)) ||
		(!starting && !d.draining) {
		d.mu.Unlock()
		return ErrLifecycleState
	}
	if !starting {
		l.stopping = true
		if owner.kind == LifecycleOwnerImplementation {
			l.ready[owner.name] = false
		}
	}
	bounded, cancel := context.WithCancel(ctx)
	scope := &hookScope{dispatcher: d, context: bounded, cancel: cancel, active: true, running: true}
	d.scope = scope
	d.idle = make(chan struct{})
	d.idleClosed = false
	idle := d.idle
	d.mu.Unlock()

	defer func() {
		cancel()
		d.mu.Lock()
		if !scope.returned {
			// Goexit unwinds the hook without returning an outcome.
			err = ErrLifecycleHook
			scope.active = false
			scope.returned = true
		}
		scope.running = false
		d.signalIdleLocked()
		if len(d.attempts) == 0 {
			d.scope = nil
		}
		if starting && err == nil {
			if ctx.Err() != nil {
				err = ctx.Err()
			} else if d.draining {
				err = ErrDispatcherDraining
			} else {
				if owner.kind == LifecycleOwnerImplementation {
					l.ready[owner.name] = true
				}
				l.next++
			}
		}
		if starting && err != nil {
			l.stopping = true
		}
		d.mu.Unlock()
	}()

	err = invokeLifecycleHook(context.WithValue(bounded, hookScopeKey{}, scope), hook)
	d.mu.Lock()
	scope.active = false
	scope.returned = true
	d.signalIdleLocked()
	alreadyIdle := d.idleClosed
	d.mu.Unlock()
	cancel()
	if alreadyIdle {
		return err
	}
	select {
	case <-idle:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func invokeLifecycleHook(ctx context.Context, hook func(context.Context) error) (err error) {
	defer func() {
		if recover() != nil {
			err = ErrLifecycleHook
		}
	}()
	if hook(ctx) != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
		return ErrLifecycleHook
	}
	return nil
}

func scopeFrom(ctx context.Context) *hookScope {
	if ctx == nil {
		return nil
	}
	scope, _ := ctx.Value(hookScopeKey{}).(*hookScope)
	return scope
}

func (d *Dispatcher) signalIdleLocked() {
	if d.idleClosed || len(d.attempts) != 0 || d.scope != nil && !d.scope.returned {
		return
	}
	if d.idle != nil {
		close(d.idle)
	}
	d.idleClosed = true
	if d.scope != nil && !d.scope.running {
		d.scope = nil
	}
}

func (d *Dispatcher) attemptAllowedLocked(attempt *targetAttempt) bool {
	if attempt.scope != nil {
		return d.scope == attempt.scope && attempt.scope.active
	}
	return d.admissionOpen && !d.draining
}
