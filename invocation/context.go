package invocation

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ErrInvalidInvocationContext reports a nil, corrupt, or internally
// contradictory invocation context.
var ErrInvalidInvocationContext = errors.New("invalid runtime invocation context")

type runtimeFrameKey struct{}

// runtimeFrame is immutable Kernel-owned ancestry and deadline state. Ordinary
// Go context remains the carrier; application identity is not part of the frame.
type runtimeFrame struct {
	requestID    RequestID
	traceID      TraceID
	invocationID InvocationID
	parentID     InvocationID
	deadline     time.Time
	authority    context.Context
	retryOwner   InvocationID
	attempt      int
}

// InvocationContext is the read-only identity-neutral runtime frame visible to
// a provider during one call.
type InvocationContext struct {
	requestID    RequestID
	traceID      TraceID
	invocationID InvocationID
	parentID     InvocationID
	deadline     time.Time
	retryOwner   InvocationID
	attempt      int
}

// RequestIDFromContext returns the runtime-owned root request identity when ctx
// is an active provider invocation context.
func RequestIDFromContext(ctx context.Context) (RequestID, bool) {
	frame, exists := runtimeFrameFrom(ctx)
	if !exists {
		return RequestID{}, false
	}
	return frame.requestID, true
}

// TraceIDFromContext returns the runtime-owned invocation call-chain identity
// when ctx is an active provider invocation context.
func TraceIDFromContext(ctx context.Context) (TraceID, bool) {
	frame, exists := runtimeFrameFrom(ctx)
	if !exists {
		return TraceID{}, false
	}
	return frame.traceID, true
}

// Current returns the Kernel frame for the current provider invocation.
func Current(ctx context.Context) (InvocationContext, bool) {
	frame, exists := runtimeFrameFrom(ctx)
	if !exists {
		return InvocationContext{}, false
	}
	return InvocationContext{
		requestID:    frame.requestID,
		traceID:      frame.traceID,
		invocationID: frame.invocationID,
		parentID:     frame.parentID,
		deadline:     frame.deadline,
		retryOwner:   frame.retryOwner,
		attempt:      frame.attempt,
	}, true
}

// RequestID returns the root request identity.
func (c InvocationContext) RequestID() RequestID { return c.requestID }

// TraceID returns the invocation call-chain identity.
func (c InvocationContext) TraceID() TraceID { return c.traceID }

// InvocationID returns the current invocation identity.
func (c InvocationContext) InvocationID() InvocationID { return c.invocationID }

// ParentInvocationID returns the immediate parent invocation, or zero for the
// first provider call in a request.
func (c InvocationContext) ParentInvocationID() InvocationID { return c.parentID }

// Deadline returns the effective invocation deadline, or zero when unbounded.
func (c InvocationContext) Deadline() time.Time { return c.deadline }

// RetryOwner identifies the single framework retry owner in this call chain.
func (c InvocationContext) RetryOwner() InvocationID { return c.retryOwner }

// Attempt returns the one-based bounded logical-call attempt number.
func (c InvocationContext) Attempt() int { return c.attempt }

func enterInvocationContext(parent context.Context, invocationID InvocationID, timeout time.Duration, started time.Time) (context.Context, func(), error) {
	if parent == nil || !invocationID.Valid() || timeout < 0 {
		return nil, nil, ErrInvalidInvocationContext
	}

	var (
		requestID         RequestID
		traceID           TraceID
		parentID          InvocationID
		inheritedDeadline time.Time
		authority         context.Context
		retryOwner        InvocationID
	)
	stored := parent.Value(runtimeFrameKey{})
	if stored != nil {
		frame, valid := stored.(runtimeFrame)
		if !valid || !frame.valid() || invocationID == frame.invocationID || invocationID == frame.parentID {
			return nil, nil, ErrInvalidInvocationContext
		}
		requestID = frame.requestID
		traceID = frame.traceID
		parentID = frame.invocationID
		inheritedDeadline = frame.deadline
		authority = frame.authority
		retryOwner = frame.retryOwner
	} else {
		var err error
		requestID, err = NewRequestID()
		if err != nil {
			return nil, nil, fmt.Errorf("%w: generate request identity: %v", ErrInvalidInvocationContext, err)
		}
		for {
			traceID, err = NewTraceID()
			if err != nil {
				return nil, nil, fmt.Errorf("%w: generate trace identity: %v", ErrInvalidInvocationContext, err)
			}
			if traceID.String() != requestID.String() {
				break
			}
		}
		authority = parent
	}

	var deadline time.Time
	if timeout > 0 {
		deadline = started.Add(timeout)
	}
	if callerDeadline, hasDeadline := parent.Deadline(); hasDeadline && (deadline.IsZero() || callerDeadline.Before(deadline)) {
		deadline = callerDeadline
	}
	if !inheritedDeadline.IsZero() && (deadline.IsZero() || inheritedDeadline.Before(deadline)) {
		deadline = inheritedDeadline
	}
	next := runtimeFrame{
		requestID:    requestID,
		traceID:      traceID,
		invocationID: invocationID,
		parentID:     parentID,
		deadline:     deadline,
		authority:    authority,
		retryOwner:   retryOwner,
		attempt:      1,
	}
	if !next.valid() {
		return nil, nil, ErrInvalidInvocationContext
	}

	var callContext context.Context
	var cancel context.CancelFunc
	if deadline.IsZero() {
		callContext, cancel = context.WithCancel(parent)
	} else {
		callContext, cancel = context.WithDeadline(parent, deadline)
	}
	stopAuthority := context.AfterFunc(authority, cancel)
	if authority.Err() != nil {
		cancel()
	}
	entered := context.WithValue(callContext, runtimeFrameKey{}, next)
	var cleanupOnce sync.Once
	cleanup := func() {
		cleanupOnce.Do(func() {
			stopAuthority()
			cancel()
		})
	}
	return entered, cleanup, nil
}

func runtimeFrameFrom(ctx context.Context) (runtimeFrame, bool) {
	if ctx == nil {
		return runtimeFrame{}, false
	}
	frame, exists := ctx.Value(runtimeFrameKey{}).(runtimeFrame)
	return frame, exists && frame.valid()
}

func (f runtimeFrame) valid() bool {
	if !f.requestID.Valid() || !f.traceID.Valid() || !f.invocationID.Valid() ||
		(f.parentID.Valid() && f.parentID == f.invocationID) || f.authority == nil || f.attempt < 1 || f.attempt > MaximumRetryAttempts {
		return false
	}
	if authorityDeadline, hasDeadline := f.authority.Deadline(); hasDeadline && (f.deadline.IsZero() || f.deadline.After(authorityDeadline)) {
		return false
	}
	return true
}
