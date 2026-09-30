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
)

// Lifecycle coordinates one dispatcher's already-constructed lifecycle values.
// Assembly supplies the complete dependency-first constructor order before
// publication. Unlisted bindings have no lifecycle work and are ready after
// construction. The lifecycle manager normally owns this assembly API.
type Lifecycle struct {
	dispatcher *Dispatcher
	order      []string
	ready      map[string]bool
	next       int
	stopping   bool
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
// Constructor identity is shared by all Interface bindings selecting it.
func (d *Dispatcher) BindLifecycle(constructors []string) (*Lifecycle, error) {
	if !d.valid() {
		return nil, ErrInvalidDispatcher
	}
	ready := make(map[string]bool, len(constructors))
	for _, constructor := range constructors {
		separator := strings.LastIndexByte(constructor, '.')
		if separator <= 0 || !validConstructorSymbol(constructor[:separator], constructor) {
			return nil, ErrInvalidLifecycle
		}
		if _, duplicate := ready[constructor]; duplicate {
			return nil, ErrInvalidLifecycle
		}
		ready[constructor] = false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.lifecycle != nil || d.catalog.Load() != nil || d.draining {
		return nil, ErrInvalidLifecycle
	}
	lifecycle := &Lifecycle{dispatcher: d, order: append([]string(nil), constructors...), ready: ready}
	d.lifecycle = lifecycle
	return lifecycle, nil
}

// Start invokes one constructor's Start hook in the supplied order. Only ready
// dependencies are callable through its context; public admission stays closed.
// Success requires the hook and all its target work to finish within ctx.
func (l *Lifecycle) Start(ctx context.Context, constructor string, hook func(context.Context) error) error {
	return l.run(ctx, constructor, hook, true)
}

// Stop invokes cleanup after Drain, with access only to dependencies that have
// not begun stopping. It supports never-started values and repeated failed
// cleanup. The lifecycle manager owns reverse ordering and successful-stop
// retention. A nil hook result remains successful after cancellation only if
// all its target work has terminated.
func (l *Lifecycle) Stop(ctx context.Context, constructor string, hook func(context.Context) error) error {
	return l.run(ctx, constructor, hook, false)
}

// Drain closes public admission and drains hooks and target attempts before
// cleanup. A failed wait must leave every pending dependency live.
func (l *Lifecycle) Drain(ctx context.Context) error {
	if l == nil || !l.dispatcher.valid() {
		return ErrInvalidLifecycle
	}
	return l.dispatcher.Drain(ctx)
}

func (l *Lifecycle) run(ctx context.Context, constructor string, hook func(context.Context) error, starting bool) (err error) {
	if l == nil || !l.dispatcher.valid() {
		return ErrInvalidLifecycle
	}
	if ctx == nil || hook == nil {
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
	_, member := l.ready[constructor]
	if d.lifecycle != l || !member || d.scope != nil || len(d.attempts) != 0 ||
		(starting && (l.stopping || d.draining || d.admissionOpen || d.catalog.Load() == nil || l.next >= len(l.order) || l.order[l.next] != constructor)) ||
		(!starting && !d.draining) {
		d.mu.Unlock()
		return ErrLifecycleState
	}
	if !starting {
		l.stopping = true
		l.ready[constructor] = false
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
				l.ready[constructor] = true
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
