package invocation

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/plystra/kernel/audit"
)

// ErrInvalidInvocationContext reports a missing, corrupt, or untrusted runtime
// invocation frame.
var ErrInvalidInvocationContext = errors.New("invalid runtime invocation context")

type runtimeFrameKey struct{}

// runtimeFrame is immutable Kernel-owned ancestry and subject state. The
// ordinary Go context remains the value carrier but cannot fabricate this key.
type runtimeFrame struct {
	requestID    audit.RequestID
	traceID      audit.TraceID
	invocationID audit.InvocationID
	parentID     audit.InvocationID
	subject      audit.SubjectContext
	deadline     time.Time
	authority    context.Context
}

// InvocationContext is the read-only governed context visible to a provider.
type InvocationContext struct {
	requestID    audit.RequestID
	traceID      audit.TraceID
	invocationID audit.InvocationID
	parentID     audit.InvocationID
	subject      audit.SubjectContext
	deadline     time.Time
}

// NewRootContext mints one root request frame through a Kernel caller scope.
// Plugin scopes cannot create roots, and an existing frame cannot be replaced.
func (s Scope) NewRootContext(parent context.Context, subject audit.SubjectContext) (context.Context, error) {
	if !s.valid() || s.caller.Kind() != audit.CallerKindKernel || parent == nil || !subject.Valid() {
		return nil, ErrInvalidInvocationContext
	}
	if parent.Value(runtimeFrameKey{}) != nil {
		return nil, ErrInvalidInvocationContext
	}
	requestID, err := audit.NewRequestID()
	if err != nil {
		return nil, fmt.Errorf("%w: generate request identity: %v", ErrInvalidInvocationContext, err)
	}
	traceID, err := audit.NewTraceID()
	if err != nil {
		return nil, fmt.Errorf("%w: generate trace identity: %v", ErrInvalidInvocationContext, err)
	}
	deadline, _ := parent.Deadline()
	frame := runtimeFrame{
		requestID: requestID,
		traceID:   traceID,
		subject:   subject,
		deadline:  deadline,
		authority: parent,
	}
	if !frame.validRoot() {
		return nil, ErrInvalidInvocationContext
	}
	return context.WithValue(parent, runtimeFrameKey{}, frame), nil
}

// RequestID returns the runtime-owned root request identity.
func RequestID(ctx context.Context) (audit.RequestID, bool) {
	frame, exists := runtimeFrameFrom(ctx)
	if !exists {
		return audit.RequestID{}, false
	}
	return frame.requestID, true
}

// TraceID returns the runtime-owned invocation call-chain identity.
func TraceID(ctx context.Context) (audit.TraceID, bool) {
	frame, exists := runtimeFrameFrom(ctx)
	if !exists {
		return audit.TraceID{}, false
	}
	return frame.traceID, true
}

// Current returns the governed frame for the current provider invocation.
// Root request frames are intentionally not provider-visible invocations.
func Current(ctx context.Context) (InvocationContext, bool) {
	frame, exists := runtimeFrameFrom(ctx)
	if !exists || !frame.invocationID.Valid() {
		return InvocationContext{}, false
	}
	return InvocationContext{
		requestID:    frame.requestID,
		traceID:      frame.traceID,
		invocationID: frame.invocationID,
		parentID:     frame.parentID,
		subject:      frame.subject,
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

// SubjectIdentity returns the optional authenticated subject reference.
func (c InvocationContext) SubjectIdentity() string { return c.subject.SubjectIdentity() }

// TenantIdentity returns the optional tenant or authorization-space reference.
func (c InvocationContext) TenantIdentity() string { return c.subject.TenantIdentity() }

// Deadline returns the effective governed invocation deadline.
func (c InvocationContext) Deadline() time.Time { return c.deadline }

func enterInvocationContext(parent context.Context, invocationID audit.InvocationID, defaultTimeout time.Duration) (context.Context, func(), error) {
	if parent == nil || !invocationID.Valid() || defaultTimeout <= 0 {
		return nil, nil, ErrInvalidInvocationContext
	}
	frame, exists := runtimeFrameFrom(parent)
	if !exists || invocationID == frame.invocationID || invocationID == frame.parentID {
		return nil, nil, ErrInvalidInvocationContext
	}

	deadline := time.Now().Add(defaultTimeout)
	if callerDeadline, hasDeadline := parent.Deadline(); hasDeadline && callerDeadline.Before(deadline) {
		deadline = callerDeadline
	}
	if !frame.deadline.IsZero() && frame.deadline.Before(deadline) {
		deadline = frame.deadline
	}
	next := runtimeFrame{
		requestID:    frame.requestID,
		traceID:      frame.traceID,
		invocationID: invocationID,
		parentID:     frame.invocationID,
		subject:      frame.subject,
		deadline:     deadline,
		authority:    frame.authority,
	}
	if !next.validInvocation() {
		return nil, nil, ErrInvalidInvocationContext
	}

	callContext, cancel := context.WithDeadline(parent, deadline)
	stopAuthority := context.AfterFunc(frame.authority, cancel)
	if frame.authority.Err() != nil {
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

func (f runtimeFrame) validRoot() bool {
	return f.valid() && !f.invocationID.Valid()
}

func (f runtimeFrame) validInvocation() bool {
	return f.valid() && f.invocationID.Valid()
}

func (f runtimeFrame) valid() bool {
	if !f.requestID.Valid() || !f.traceID.Valid() || !f.subject.Valid() || f.authority == nil {
		return false
	}
	authorityDeadline, hasDeadline := f.authority.Deadline()
	if !f.invocationID.Valid() {
		if f.parentID.Valid() {
			return false
		}
		if !hasDeadline {
			return f.deadline.IsZero()
		}
		return !f.deadline.IsZero() && f.deadline.Equal(authorityDeadline)
	}
	if f.deadline.IsZero() || f.invocationID == f.parentID {
		return false
	}
	return !hasDeadline || !f.deadline.After(authorityDeadline)
}
