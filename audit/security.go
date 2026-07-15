package audit

import "errors"

// MaximumSecurityContextReferenceSize bounds each optional opaque security or
// authorization reference propagated with an invocation.
const MaximumSecurityContextReferenceSize = 256

// ErrInvalidSecurityContext reports an incomplete or unsafe governed security
// context.
var ErrInvalidSecurityContext = errors.New("invalid runtime security context")

// SecurityContextOptions describes the provider-neutral facts established by a
// trusted ingress boundary. SubjectPrincipal may be zero to use CallerPrincipal.
type SecurityContextOptions struct {
	CallerPrincipal                Principal
	SubjectPrincipal               Principal
	MemberReference                string
	AuthorizationBoundaryReference string
	AuthenticationContextReference string
	AuthorizationContextReference  string
}

// SecurityContext is the immutable provider-neutral security state propagated
// through governed invocation. Optional references remain opaque to the Kernel.
type SecurityContext struct {
	callerPrincipal                Principal
	subjectPrincipal               Principal
	memberReference                string
	authorizationBoundaryReference string
	authenticationContextReference string
	authorizationContextReference  string
}

// NewSecurityContext validates and copies trusted provider-neutral security
// facts. An omitted subject is normalized to the caller so downstream code
// always receives complete Principal references.
func NewSecurityContext(options SecurityContextOptions) (SecurityContext, error) {
	subject := options.SubjectPrincipal
	if subject == (Principal{}) {
		subject = options.CallerPrincipal
	}
	security := SecurityContext{
		callerPrincipal:                options.CallerPrincipal,
		subjectPrincipal:               subject,
		memberReference:                options.MemberReference,
		authorizationBoundaryReference: options.AuthorizationBoundaryReference,
		authenticationContextReference: options.AuthenticationContextReference,
		authorizationContextReference:  options.AuthorizationContextReference,
	}
	if !security.Valid() {
		return SecurityContext{}, ErrInvalidSecurityContext
	}
	return security, nil
}

// CallerPrincipal returns the Principal that initiated the governed request.
func (c SecurityContext) CallerPrincipal() Principal {
	if !c.Valid() {
		return Principal{}
	}
	return c.callerPrincipal
}

// SubjectPrincipal returns the Principal acted on behalf of or targeted by the
// request. It equals CallerPrincipal when no distinct subject was established.
func (c SecurityContext) SubjectPrincipal() Principal {
	if !c.Valid() {
		return Principal{}
	}
	return c.subjectPrincipal
}

// MemberReference returns the optional opaque authorization membership reference.
func (c SecurityContext) MemberReference() string {
	if !c.Valid() {
		return ""
	}
	return c.memberReference
}

// AuthorizationBoundaryReference returns the optional opaque Space,
// Organization, tenant, or equivalent authorization-boundary reference.
func (c SecurityContext) AuthorizationBoundaryReference() string {
	if !c.Valid() {
		return ""
	}
	return c.authorizationBoundaryReference
}

// AuthenticationContextReference returns the optional opaque AuthN context reference.
func (c SecurityContext) AuthenticationContextReference() string {
	if !c.Valid() {
		return ""
	}
	return c.authenticationContextReference
}

// AuthorizationContextReference returns the optional opaque AuthZ context reference.
func (c SecurityContext) AuthorizationContextReference() string {
	if !c.Valid() {
		return ""
	}
	return c.authorizationContextReference
}

// Valid reports whether the context contains complete Principals and bounded
// safe optional references.
func (c SecurityContext) Valid() bool {
	return c.callerPrincipal.Valid() && c.subjectPrincipal.Valid() &&
		validOptionalSecurityReference(c.memberReference) &&
		validOptionalSecurityReference(c.authorizationBoundaryReference) &&
		validOptionalSecurityReference(c.authenticationContextReference) &&
		validOptionalSecurityReference(c.authorizationContextReference)
}

func validOptionalSecurityReference(value string) bool {
	return value == "" || validPrincipalReference(value, MaximumSecurityContextReferenceSize)
}
