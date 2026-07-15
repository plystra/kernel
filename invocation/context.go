package invocation

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/plystra/kernel/audit"
)

// ErrInvalidInvocationContext reports a missing, nested, or untrusted runtime
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

func runtimeFrameFrom(ctx context.Context) (runtimeFrame, bool) {
	if ctx == nil {
		return runtimeFrame{}, false
	}
	frame, exists := ctx.Value(runtimeFrameKey{}).(runtimeFrame)
	return frame, exists && frame.validRoot()
}

func (f runtimeFrame) validRoot() bool {
	if !f.requestID.Valid() || !f.traceID.Valid() || f.invocationID.Valid() || f.parentID.Valid() || !f.subject.Valid() || f.authority == nil {
		return false
	}
	authorityDeadline, hasDeadline := f.authority.Deadline()
	if !hasDeadline {
		return f.deadline.IsZero()
	}
	return !f.deadline.IsZero() && f.deadline.Equal(authorityDeadline)
}
