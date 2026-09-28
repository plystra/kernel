package invocation

import (
	"context"
	"errors"
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
}

func (d *Dispatcher) registerAttempt(attempt *targetAttempt) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.draining {
		return false
	}
	d.attempts[attempt] = struct{}{}
	return true
}

func (d *Dispatcher) enterAttempt(ctx context.Context, attempt *targetAttempt) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.draining || attempt.abandoned || ctx.Err() != nil {
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
	if d.draining && len(d.attempts) == 0 {
		close(d.drained)
	}
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

// AdmissionClosed reports whether shutdown has permanently closed admission.
// Invalid dispatchers are closed. Published remains an independent catalog fact.
func (d *Dispatcher) AdmissionClosed() bool {
	if !d.valid() {
		return true
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.draining
}

// Drain permanently closes admission, cancels every registered target context,
// and waits for actual adapter and response-processor termination. It requires
// a deadline-bound context and never stops lifecycle dependencies itself. A
// failed drain must leave those dependencies live; retry with a fresh bounded
// context. Concurrent drain callers share termination state but keep their own
// deadlines.
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
		for attempt := range d.attempts {
			cancels = append(cancels, attempt.cancel)
		}
		if len(d.attempts) == 0 {
			close(d.drained)
		}
	}
	d.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	if err := ctx.Err(); err != nil {
		return drainContextError(err)
	}
	select {
	case <-d.drained:
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
