package invocation

import (
	"context"
	"errors"

	"github.com/plystra/kernel/audit"
	"github.com/plystra/kernel/capability"
)

var (
	// ErrInvalidAuthorization reports a missing policy or incomplete governed
	// authorization input.
	ErrInvalidAuthorization = errors.New("invalid capability authorization")
	// ErrAuthorizationFailed reports an authorizer error, panic, or invalid
	// decision without retaining policy implementation details.
	ErrAuthorizationFailed = errors.New("capability authorization failed")
)

// AuthorizationRequest is an immutable Kernel-owned capability policy input.
// It deliberately identifies the requested capability rather than the selected
// provider so provider replacement cannot change caller authorization.
type AuthorizationRequest struct {
	caller       audit.CallerIdentity
	capability   capability.Identifier
	requestID    audit.RequestID
	traceID      audit.TraceID
	invocationID audit.InvocationID
	parentID     audit.InvocationID
	subject      audit.SubjectContext
}

// Authorizer applies runtime policy to one governed capability request. It must
// return an explicit valid allow or deny decision; errors and panics fail closed.
type Authorizer func(context.Context, AuthorizationRequest) (AuthorizationDecision, error)

type authorizationOutcome uint8

const (
	authorizationAllowed authorizationOutcome = iota + 1
	authorizationDenied
)

// AuthorizationDecision is an immutable explicit policy result. Its zero value
// is invalid and therefore cannot silently authorize a capability.
type AuthorizationDecision struct {
	outcome    authorizationOutcome
	denialCode string
}

// NewAllowedAuthorization creates an explicit allow decision.
func NewAllowedAuthorization() AuthorizationDecision {
	return AuthorizationDecision{outcome: authorizationAllowed}
}

// NewDeniedAuthorization creates an explicit denial with one safe stable reason.
func NewDeniedAuthorization(denialCode string) (AuthorizationDecision, error) {
	decision := AuthorizationDecision{outcome: authorizationDenied, denialCode: denialCode}
	if !decision.Valid() {
		return AuthorizationDecision{}, ErrInvalidAuthorization
	}
	return decision, nil
}

// Allowed reports whether this is an explicit valid allow decision.
func (d AuthorizationDecision) Allowed() bool {
	return d.Valid() && d.outcome == authorizationAllowed
}

// DenialCode returns the safe stable reason for a denied decision.
func (d AuthorizationDecision) DenialCode() string {
	if !d.Valid() || d.outcome != authorizationDenied {
		return ""
	}
	return d.denialCode
}

// Valid reports whether the decision has exactly one safe explicit outcome.
func (d AuthorizationDecision) Valid() bool {
	switch d.outcome {
	case authorizationAllowed:
		return d.denialCode == ""
	case authorizationDenied:
		return d.denialCode != "" && validDetailCode(d.denialCode)
	default:
		return false
	}
}

// Caller returns the logical Kernel or plugin caller provenance.
func (r AuthorizationRequest) Caller() audit.CallerIdentity {
	if !r.Valid() {
		return audit.CallerIdentity{}
	}
	return r.caller
}

// Capability returns the exact provider-independent capability identity.
func (r AuthorizationRequest) Capability() capability.Identifier {
	if !r.Valid() {
		return capability.Identifier{}
	}
	return r.capability
}

// RequestID returns the root request identity.
func (r AuthorizationRequest) RequestID() audit.RequestID {
	if !r.Valid() {
		return audit.RequestID{}
	}
	return r.requestID
}

// TraceID returns the invocation call-chain identity.
func (r AuthorizationRequest) TraceID() audit.TraceID {
	if !r.Valid() {
		return audit.TraceID{}
	}
	return r.traceID
}

// InvocationID returns the current audited invocation identity.
func (r AuthorizationRequest) InvocationID() audit.InvocationID {
	if !r.Valid() {
		return audit.InvocationID{}
	}
	return r.invocationID
}

// ParentInvocationID returns the immediate parent invocation, or zero for the
// first capability call in a request.
func (r AuthorizationRequest) ParentInvocationID() audit.InvocationID {
	if !r.Valid() {
		return audit.InvocationID{}
	}
	return r.parentID
}

// SubjectIdentity returns the optional authenticated subject reference.
func (r AuthorizationRequest) SubjectIdentity() string {
	if !r.Valid() {
		return ""
	}
	return r.subject.SubjectIdentity()
}

// TenantIdentity returns the optional tenant or authorization-space reference.
func (r AuthorizationRequest) TenantIdentity() string {
	if !r.Valid() {
		return ""
	}
	return r.subject.TenantIdentity()
}

// Valid reports whether this request contains one complete governed invocation.
func (r AuthorizationRequest) Valid() bool {
	return r.caller.Valid() && r.capability.String() != "" && r.requestID.Valid() && r.traceID.Valid() &&
		r.invocationID.Valid() && r.subject.Valid() && r.invocationID != r.parentID
}

func newAuthorizationRequest(ctx context.Context, scope Scope, identifier capability.Identifier) (AuthorizationRequest, error) {
	if !scope.valid() || identifier.String() == "" {
		return AuthorizationRequest{}, ErrInvalidAuthorization
	}
	frame, exists := runtimeFrameFrom(ctx)
	if !exists || !frame.validInvocation() {
		return AuthorizationRequest{}, ErrInvalidAuthorization
	}
	request := AuthorizationRequest{
		caller:       scope.caller,
		capability:   identifier,
		requestID:    frame.requestID,
		traceID:      frame.traceID,
		invocationID: frame.invocationID,
		parentID:     frame.parentID,
		subject:      frame.subject,
	}
	if !request.Valid() {
		return AuthorizationRequest{}, ErrInvalidAuthorization
	}
	return request, nil
}

func authorizeCapability(ctx context.Context, authorizer Authorizer, request AuthorizationRequest) (decision AuthorizationDecision, err error) {
	if ctx == nil || authorizer == nil || !request.Valid() {
		return AuthorizationDecision{}, ErrInvalidAuthorization
	}
	defer func() {
		if recover() != nil {
			decision = AuthorizationDecision{}
			err = ErrAuthorizationFailed
		}
	}()
	decision, err = authorizer(ctx, request)
	if err != nil || !decision.Valid() {
		return AuthorizationDecision{}, ErrAuthorizationFailed
	}
	return decision, nil
}
