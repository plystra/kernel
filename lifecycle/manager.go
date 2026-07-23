package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
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

// ManagerOptions configures bounded cleanup after a failed startup.
type ManagerOptions struct {
	RollbackTimeout time.Duration
}

// Manager starts lifecycle instances in generated order and stops active
// instances in reverse order. It never discovers or reorders Implementations.
type Manager struct {
	mu              sync.RWMutex
	bindings        []Binding
	active          []bool
	state           State
	rollbackTimeout time.Duration
}

// NewManager validates and defensively copies an already-resolved lifecycle
// order. Constructor symbols must be unique.
func NewManager(options ManagerOptions, bindings []Binding) (*Manager, error) {
	if options.RollbackTimeout <= 0 {
		return nil, ErrInvalidManager
	}
	seen := make(map[string]struct{}, len(bindings))
	ordered := make([]Binding, len(bindings))
	for index, binding := range bindings {
		if !binding.valid() {
			return nil, fmt.Errorf("%w: binding %d: %w", ErrInvalidManager, index, ErrInvalidBinding)
		}
		if _, duplicate := seen[binding.constructor]; duplicate {
			return nil, fmt.Errorf("%w: duplicate constructor %s", ErrInvalidManager, binding.constructor)
		}
		seen[binding.constructor] = struct{}{}
		ordered[index] = binding
	}
	return &Manager{
		bindings:        ordered,
		active:          make([]bool, len(ordered)),
		state:           StateNew,
		rollbackTimeout: options.RollbackTimeout,
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
// bounded reverse-order rollback that includes the failing instance in case it
// acquired resources before returning or panicking.
func (m *Manager) Start(ctx context.Context) error {
	if m == nil {
		return ErrInvalidManager
	}
	if ctx == nil {
		return ErrInvalidContext
	}
	if err := m.transition(StateNew, StateStarting); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		m.setState(StateFailed)
		return operationFailure(ErrStart, "", err)
	}

	for index, binding := range m.bindings {
		if err := ctx.Err(); err != nil {
			rollback := m.rollback(ctx, index-1)
			m.setState(StateFailed)
			return errors.Join(operationFailure(ErrStart, "", err), rollback)
		}
		m.active[index] = true
		if err := invokeHook(ctx, binding.instance.Start); err != nil {
			rollback := m.rollback(ctx, index)
			m.setState(StateFailed)
			return errors.Join(operationFailure(ErrStart, "constructor "+binding.constructor, err), rollback)
		}
	}
	m.setState(StateRunning)
	return nil
}

// Stop invokes every active instance in reverse generated order. Successful
// instances are not called again; failed stops may be retried by calling Stop
// with a fresh context.
func (m *Manager) Stop(ctx context.Context) error {
	if m == nil {
		return ErrInvalidManager
	}
	if ctx == nil {
		return ErrInvalidContext
	}

	m.mu.Lock()
	switch m.state {
	case StateNew:
		m.state = StateStopped
		m.mu.Unlock()
		return nil
	case StateStopped:
		m.mu.Unlock()
		return nil
	case StateRunning, StateFailed:
		m.state = StateStopping
		m.mu.Unlock()
	default:
		state := m.state
		m.mu.Unlock()
		return stateFailure("stop", state)
	}

	err := m.stopActive(ctx, len(m.bindings)-1)
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

func (m *Manager) rollback(parent context.Context, last int) error {
	if last < 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), m.rollbackTimeout)
	defer cancel()
	return m.stopActive(ctx, last)
}

func (m *Manager) stopActive(ctx context.Context, last int) error {
	failed := make([]string, 0)
	var cause error
	for index := last; index >= 0; index-- {
		if !m.active[index] {
			continue
		}
		binding := m.bindings[index]
		if err := invokeHook(ctx, binding.instance.Stop); err != nil {
			failed = append(failed, binding.constructor)
			if contextError(err) != nil {
				cause = contextError(err)
			}
			continue
		}
		m.active[index] = false
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
	if err := ctx.Err(); err != nil {
		return err
	}
	if hookErr != nil {
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
