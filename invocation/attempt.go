package invocation

import (
	"context"
	"errors"

	"github.com/plystra/kernel/capability"
)

var (
	// ErrInvalidDrainContext reports a nil context or missing shutdown deadline.
	ErrInvalidDrainContext = errors.New("dispatcher drain requires a deadline-bound context")
	// ErrDrain reports an incomplete invocation drain. Only a standard context
	// cancellation or deadline cause may accompany it.
	ErrDrain = errors.New("capability dispatcher drain incomplete")
)

// All mutable attempt state is protected by the owning dispatcher's mutex.
type targetAttempt struct {
	cancel    func()
	entered   bool
	abandoned bool
	bindingID capability.Identifier
	scope     *hookScope
}

func (d *Dispatcher) registerAttempt(ctx context.Context, attempt *targetAttempt, binding Binding) *Error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if boundary := invocationContextError(ctx); boundary != nil {
		return boundary
	}
	if boundary := d.admissionBoundaryLocked(ctx, binding); boundary != nil {
		return boundary
	}
	identifier := binding.endpoint.definition.Identifier()
	if d.inflight[identifier] >= binding.policy.ConcurrencyLimit {
		return newNotStartedBoundary(ErrorResourceExhausted, detailConcurrencyExhausted)
	}
	attempt.bindingID = identifier
	if scope := scopeFrom(ctx); scope != nil && scope.dispatcher == d {
		attempt.scope = scope
	}
	if d.idleClosed {
		// Ordinary calls need no wait channel until a drain observes them.
		d.idle = nil
		d.idleClosed = false
	}
	d.inflight[identifier]++
	d.attempts[attempt] = struct{}{}
	return nil
}

func (d *Dispatcher) enterAttempt(ctx context.Context, attempt *targetAttempt) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.attemptAllowedLocked(attempt) || attempt.abandoned || invocationContextError(ctx) != nil {
		return false
	}
	attempt.entered = true
	return true
}

func (d *Dispatcher) abandonAttempt(attempt *targetAttempt, boundary *Error) *Error {
	d.mu.Lock()
	defer d.mu.Unlock()
	attempt.abandoned = true
	if attempt.entered {
		boundary.completion = CompletionResultUnknown
	}
	return boundary
}

func (d *Dispatcher) finishAttempt(attempt *targetAttempt) {
	d.mu.Lock()
	defer d.mu.Unlock()
	delete(d.attempts, attempt)
	d.inflight[attempt.bindingID]--
	if d.inflight[attempt.bindingID] == 0 {
		delete(d.inflight, attempt.bindingID)
	}
	d.signalIdleLocked()
}

// ActiveAttempts reports registered adapter executions, including scheduled
// attempts and attempts whose callers have already completed. An attempt is
// removed only after its adapter and any response processor finish, panic, or
// exit their goroutine.
func (d *Dispatcher) ActiveAttempts() int {
	if !d.valid() {
		return 0
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.attempts)
}

// AdmissionClosed reports whether shutdown has permanently closed public admission.
// Invalid dispatchers are closed. Published remains an independent catalog fact.
// Before startup it returns false; use Accepting to test current admission.
func (d *Dispatcher) AdmissionClosed() bool {
	if !d.valid() {
		return true
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.draining
}

// Drain permanently closes public admission, cancels active hooks and targets,
// and waits for hook bodies, adapters, and response processors to terminate.
// It requires a deadline-bound context and never stops lifecycle dependencies itself. A
// failed drain must leave those dependencies live; retry with a fresh bounded
// context. Concurrent drain callers share termination state but keep their own
// deadlines. Only subsequently entered lifecycle cleanup scopes can admit new
// dependency work; their owner must drain again before stopping dependencies.
func (d *Dispatcher) Drain(ctx context.Context) error {
	if !d.valid() {
		return ErrInvalidDispatcher
	}
	if ctx == nil {
		return ErrInvalidDrainContext
	}
	if _, bounded := ctx.Deadline(); !bounded {
		return ErrInvalidDrainContext
	}
	d.mu.Lock()
	cancels := make([]func(), 0, len(d.attempts))
	if !d.draining {
		d.draining = true
		close(d.closing)
	}
	for attempt := range d.attempts {
		cancels = append(cancels, attempt.cancel)
	}
	if d.scope != nil {
		d.scope.active = false
		cancels = append(cancels, d.scope.cancel)
	}
	if d.idle == nil {
		d.idle = make(chan struct{})
		d.idleClosed = false
		d.signalIdleLocked()
	}
	// A hook body remains an owner even after cancellation revokes its calls.
	// Its return path signals idle after the body and its targets have ended.
	idle := d.idle
	d.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	if err := ctx.Err(); err != nil {
		return drainContextError(err)
	}
	select {
	case <-idle:
		if err := ctx.Err(); err != nil {
			return drainContextError(err)
		}
		return nil
	case <-ctx.Done():
		return drainContextError(ctx.Err())
	}
}

func drainContextError(err error) error {
	switch err {
	case context.Canceled, context.DeadlineExceeded:
		return errors.Join(ErrDrain, err)
	default:
		return ErrDrain
	}
}
