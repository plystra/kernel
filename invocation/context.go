package invocation

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/plystra/kernel/audit"
)

// ErrInvalidInvocationContext reports a nil, corrupt, or internally
// contradictory invocation context.
var ErrInvalidInvocationContext = errors.New("invalid runtime invocation context")

type runtimeFrameKey struct{}

// runtimeFrame is immutable Kernel-owned ancestry and deadline state. Ordinary
// Go context remains the carrier; application identity is not part of the frame.
type runtimeFrame struct {
	requestID    audit.RequestID
	traceID      audit.TraceID
	invocationID audit.InvocationID
	parentID     audit.InvocationID
	deadline     time.Time
	authority    context.Context
}

// InvocationContext is the read-only identity-neutral runtime frame visible to
// a provider during one call.
type InvocationContext struct {
	requestID    audit.RequestID
	traceID      audit.TraceID
	invocationID audit.InvocationID
	parentID     audit.InvocationID
	deadline     time.Time
}

// RequestID returns the runtime-owned root request identity when ctx is an
// active provider invocation context.
func RequestID(ctx context.Context) (audit.RequestID, bool) {
	frame, exists := runtimeFrameFrom(ctx)
	if !exists {
		return audit.RequestID{}, false
	}
	return frame.requestID, true
}

// TraceID returns the runtime-owned invocation call-chain identity when ctx is
// an active provider invocation context.
func TraceID(ctx context.Context) (audit.TraceID, bool) {
	frame, exists := runtimeFrameFrom(ctx)
	if !exists {
		return audit.TraceID{}, false
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
	}, true
}

// RequestID returns the root request identity.
func (c InvocationContext) RequestID() audit.RequestID { return c.requestID }

// TraceID returns the invocation call-chain identity.
func (c InvocationContext) TraceID() audit.TraceID { return c.traceID }

// InvocationID returns the current invocation identity.
func (c InvocationContext) InvocationID() audit.InvocationID { return c.invocationID }

// ParentInvocationID returns the immediate parent invocation, or zero for the
// first provider call in a request.
func (c InvocationContext) ParentInvocationID() audit.InvocationID { return c.parentID }

// Deadline returns the effective invocation deadline.
func (c InvocationContext) Deadline() time.Time { return c.deadline }

func enterInvocationContext(parent context.Context, invocationID audit.InvocationID, defaultTimeout time.Duration) (context.Context, func(), error) {
	if parent == nil || !invocationID.Valid() || defaultTimeout <= 0 {
		return nil, nil, ErrInvalidInvocationContext
	}

	var (
		requestID         audit.RequestID
		traceID           audit.TraceID
		parentID          audit.InvocationID
		inheritedDeadline time.Time
		authority         context.Context
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
	} else {
		var err error
		requestID, err = audit.NewRequestID()
		if err != nil {
			return nil, nil, fmt.Errorf("%w: generate request identity: %v", ErrInvalidInvocationContext, err)
		}
		for {
			traceID, err = audit.NewTraceID()
			if err != nil {
				return nil, nil, fmt.Errorf("%w: generate trace identity: %v", ErrInvalidInvocationContext, err)
			}
			if traceID.String() != requestID.String() {
				break
			}
		}
		authority = parent
	}

	deadline := time.Now().Add(defaultTimeout)
	if callerDeadline, hasDeadline := parent.Deadline(); hasDeadline && callerDeadline.Before(deadline) {
		deadline = callerDeadline
	}
	if !inheritedDeadline.IsZero() && inheritedDeadline.Before(deadline) {
		deadline = inheritedDeadline
	}
	next := runtimeFrame{
		requestID:    requestID,
		traceID:      traceID,
		invocationID: invocationID,
		parentID:     parentID,
		deadline:     deadline,
		authority:    authority,
	}
	if !next.valid() {
		return nil, nil, ErrInvalidInvocationContext
	}

	callContext, cancel := context.WithDeadline(parent, deadline)
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
		(f.parentID.Valid() && f.parentID == f.invocationID) || f.deadline.IsZero() || f.authority == nil {
		return false
	}
	if authorityDeadline, hasDeadline := f.authority.Deadline(); hasDeadline && f.deadline.After(authorityDeadline) {
		return false
	}
	return true
}
