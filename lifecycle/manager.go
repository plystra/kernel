package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/plystra/kernel/invocation"
)

var (
	// ErrInvalidManager reports invalid lifecycle bindings or options.
	ErrInvalidManager = errors.New("invalid implementation lifecycle manager")
	// ErrInvalidContext reports a nil lifecycle operation context.
	ErrInvalidContext = errors.New("invalid implementation lifecycle context")
	// ErrState reports an operation that is not valid in the current state.
	ErrState = errors.New("invalid implementation lifecycle state")
	// ErrStart reports a redacted Implementation startup failure.
	ErrStart = errors.New("implementation lifecycle startup failed")
	// ErrStop reports one or more redacted Implementation shutdown failures.
	ErrStop = errors.New("implementation lifecycle shutdown failed")
)

// State is one closed manager lifecycle state.
type State string

const (
	StateNew      State = "new"
	StateStarting State = "starting"
	StateRunning  State = "running"
	StateStopping State = "stopping"
	StateStopped  State = "stopped"
	StateFailed   State = "failed"
)

// String returns the stable state representation, or empty for an invalid
// value.
func (s State) String() string {
	if !s.Valid() {
		return ""
	}
	return string(s)
}

// Valid reports whether the state belongs to the closed state set.
func (s State) Valid() bool {
	switch s {
	case StateNew, StateStarting, StateRunning, StateStopping, StateStopped, StateFailed:
		return true
	default:
		return false
	}
}

// ManagerOptions configures bounded rollback and governed hook dependencies.
type ManagerOptions struct {
	RollbackTimeout time.Duration
	// Dispatcher binds governed dependency calls to the supplied lifecycle
	// order before catalog publication. Nil is for lifecycles without governed
	// invocation. When set, Start and Stop require deadline-bound contexts.
	Dispatcher *invocation.Dispatcher
}

// Manager owns cleanup of every supplied constructed lifecycle instance,
// starts them in generated order, and stops them in reverse order. It never
// discovers or reorders Implementations.
type Manager struct {
	mu              sync.RWMutex
	bindings        []Binding
	pendingStop     []bool
	state           State
	rollbackTimeout time.Duration
	invocations     *invocation.Lifecycle
}

// NewManager validates and defensively copies an already-resolved lifecycle
// order. Constructor symbols must be unique. Every binding is a constructed
// value requiring Stop, even if Start is never entered.
func NewManager(options ManagerOptions, bindings []Binding) (*Manager, error) {
	if options.RollbackTimeout <= 0 {
		return nil, ErrInvalidManager
	}
	seen := make(map[string]struct{}, len(bindings))
	ordered := make([]Binding, len(bindings))
	pendingStop := make([]bool, len(bindings))
	for index, binding := range bindings {
		if !binding.valid() {
			return nil, fmt.Errorf("%w: binding %d: %w", ErrInvalidManager, index, ErrInvalidBinding)
		}
		if _, duplicate := seen[binding.constructor]; duplicate {
			return nil, fmt.Errorf("%w: duplicate constructor %s", ErrInvalidManager, binding.constructor)
		}
		seen[binding.constructor] = struct{}{}
		ordered[index] = binding
		pendingStop[index] = true
	}
	var invocations *invocation.Lifecycle
	if options.Dispatcher != nil {
		constructors := make([]string, len(ordered))
		for index, binding := range ordered {
			constructors[index] = binding.constructor
		}
		var err error
		invocations, err = options.Dispatcher.BindLifecycle(constructors)
		if err != nil {
			return nil, errors.Join(ErrInvalidManager, err)
		}
	}
	return &Manager{
		bindings:        ordered,
		pendingStop:     pendingStop,
		state:           StateNew,
		rollbackTimeout: options.RollbackTimeout,
		invocations:     invocations,
	}, nil
}

// State returns the current manager state.
func (m *Manager) State() State {
	if m == nil {
		return ""
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.state
}

// Start invokes instances in the exact generated order. A failure triggers a
// bounded reverse-order rollback of all constructed instances, including the
// failing instance and instances whose Start was never entered.
func (m *Manager) Start(ctx context.Context) error {
	if m == nil {
		return ErrInvalidManager
	}
	if ctx == nil {
		return ErrInvalidContext
	}
	if _, bounded := ctx.Deadline(); m.invocations != nil && !bounded {
		return ErrInvalidContext
	}
	if err := m.transition(StateNew, StateStarting); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		rollback := m.rollback(ctx)
		m.setState(StateFailed)
		return errors.Join(operationFailure(ErrStart, "", err), rollback)
	}

	for _, binding := range m.bindings {
		if err := ctx.Err(); err != nil {
			rollback := m.rollback(ctx)
			m.setState(StateFailed)
			return errors.Join(operationFailure(ErrStart, "", err), rollback)
		}
		var err error
		if m.invocations == nil {
			err = invokeHook(ctx, binding.instance.Start)
		} else {
			err = m.invocations.Start(ctx, binding.constructor, binding.instance.Start)
		}
		if contextErr := ctx.Err(); contextErr != nil {
			err = contextErr
		}
		if err != nil {
			rollback := m.rollback(ctx)
			m.setState(StateFailed)
			return errors.Join(operationFailure(ErrStart, "constructor "+binding.constructor, err), rollback)
		}
	}
	m.setState(StateRunning)
	return nil
}

// Stop invokes every constructed instance still requiring cleanup in reverse
// generated order, including before Start. Successful instances are not called
// again; failed stops may be retried by calling Stop with a fresh context.
// A hook returning nil confirms cleanup even if its context was cancelled
// during execution; cancellation still prevents entering remaining hooks.
func (m *Manager) Stop(ctx context.Context) error {
	if m == nil {
		return ErrInvalidManager
	}
	if ctx == nil {
		return ErrInvalidContext
	}
	if _, bounded := ctx.Deadline(); m.invocations != nil && !bounded {
		return ErrInvalidContext
	}

	m.mu.Lock()
	switch m.state {
	case StateStopped:
		m.mu.Unlock()
		return nil
	case StateNew, StateRunning, StateFailed:
		m.state = StateStopping
		m.mu.Unlock()
	default:
		state := m.state
		m.mu.Unlock()
		return stateFailure("stop", state)
	}

	err := m.stopPending(ctx)
	if err != nil {
		m.setState(StateFailed)
		return err
	}
	m.setState(StateStopped)
	return nil
}

func (m *Manager) transition(from, to State) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state != from {
		return stateFailure("start", m.state)
	}
	m.state = to
	return nil
}

func (m *Manager) setState(state State) {
	m.mu.Lock()
	m.state = state
	m.mu.Unlock()
}

func (m *Manager) rollback(parent context.Context) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), m.rollbackTimeout)
	defer cancel()
	return m.stopPending(ctx)
}

func (m *Manager) stopPending(ctx context.Context) error {
	failed := make([]string, 0)
	var cause error
	if m.invocations != nil && len(m.bindings) == 0 {
		if err := m.invocations.Drain(ctx); err != nil {
			return errors.Join(operationFailure(ErrStop, "invocation drain", ctx.Err()), err)
		}
	}
	for index := len(m.bindings) - 1; index >= 0; index-- {
		if !m.pendingStop[index] {
			continue
		}
		binding := m.bindings[index]
		var err error
		if m.invocations == nil {
			err = invokeHook(ctx, binding.instance.Stop)
		} else {
			if drainErr := m.invocations.Drain(ctx); drainErr != nil {
				var prior error
				if len(failed) != 0 {
					prior = operationFailure(ErrStop, "constructors "+strings.Join(failed, ", "), cause)
				}
				return errors.Join(prior, operationFailure(ErrStop, "invocation drain", ctx.Err()), drainErr)
			}
			err = m.invocations.Stop(ctx, binding.constructor, binding.instance.Stop)
		}
		if err != nil {
			failed = append(failed, binding.constructor)
			if contextError(err) != nil {
				cause = contextError(err)
			}
			continue
		}
		m.pendingStop[index] = false
	}
	if len(failed) == 0 {
		return nil
	}
	return operationFailure(ErrStop, "constructors "+strings.Join(failed, ", "), cause)
}

func invokeHook(ctx context.Context, hook func(context.Context) error) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	defer func() {
		if recover() != nil {
			err = errInstanceHook
		}
	}()
	hookErr := hook(ctx)
	if hookErr != nil {
		if err := ctx.Err(); err != nil {
			return err
		}
		return errInstanceHook
	}
	return nil
}

var errInstanceHook = errors.New("implementation lifecycle hook failed")

type safeOperationError struct {
	kind   error
	detail string
	cause  error
}

func operationFailure(kind error, detail string, cause error) error {
	return &safeOperationError{kind: kind, detail: detail, cause: contextError(cause)}
}

func stateFailure(operation string, state State) error {
	return fmt.Errorf("%w: cannot %s from %s", ErrState, operation, state)
}

func (e *safeOperationError) Error() string {
	message := e.kind.Error()
	if e.detail != "" {
		message += ": " + e.detail
	}
	if e.cause != nil {
		message += ": " + e.cause.Error()
	}
	return message
}

func (e *safeOperationError) Is(target error) bool {
	return e != nil && (target == e.kind || e.cause != nil && target == e.cause)
}

func contextError(err error) error {
	switch err {
	case context.Canceled:
		return context.Canceled
	case context.DeadlineExceeded:
		return context.DeadlineExceeded
	default:
		return nil
	}
}
