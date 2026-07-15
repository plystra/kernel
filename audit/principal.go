package audit

import "errors"

const (
	// MaximumPrincipalKindSize bounds an extensible principal-kind identifier.
	MaximumPrincipalKindSize = 64
	// MaximumPrincipalSubjectSize bounds an opaque principal subject reference.
	MaximumPrincipalSubjectSize = 256
	// MaximumPrincipalIssuerSize bounds an optional opaque issuer reference.
	MaximumPrincipalIssuerSize = 512
)

// ErrInvalidPrincipal reports an incomplete or unsafe security-subject
// reference.
var ErrInvalidPrincipal = errors.New("invalid runtime principal")

// PrincipalKind classifies a security subject without defining a User domain.
// Besides the standard constants, callers may use another canonical lower-case
// kind so integrations can represent domain-neutral subjects without Kernel
// changes.
type PrincipalKind string

const (
	PrincipalKindAnonymous PrincipalKind = "anonymous"
	PrincipalKindUser      PrincipalKind = "user"
	PrincipalKindService   PrincipalKind = "service"
	PrincipalKindPlugin    PrincipalKind = "plugin"
	PrincipalKindSystem    PrincipalKind = "system"
	PrincipalKindDevice    PrincipalKind = "device"
)

// String returns the canonical principal-kind identifier.
func (k PrincipalKind) String() string {
	if !k.Valid() {
		return ""
	}
	return string(k)
}

// Valid reports whether the kind is a bounded canonical extensible identifier.
func (k PrincipalKind) Valid() bool {
	value := string(k)
	if len(value) == 0 || len(value) > MaximumPrincipalKindSize || value[0] < 'a' || value[0] > 'z' {
		return false
	}
	separator := false
	for index := 1; index < len(value); index++ {
		character := value[index]
		switch {
		case character >= 'a' && character <= 'z', character >= '0' && character <= '9':
			separator = false
		case !separator && (character == '-' || character == '.'):
			separator = true
		default:
			return false
		}
	}
	return !separator
}

// Principal is an immutable domain-neutral reference to a caller or security
// subject. It contains no User, account, credential, profile, or membership
// data. Its zero value is invalid so anonymous access must be explicit.
type Principal struct {
	kind        PrincipalKind
	subject     string
	issuer      string
	initialized bool
}

// NewPrincipal validates one opaque principal reference. Anonymous principals
// must have no subject or issuer; every other kind requires a subject and may
// carry an issuer.
func NewPrincipal(kind PrincipalKind, subject, issuer string) (Principal, error) {
	principal := Principal{
		kind:        kind,
		subject:     subject,
		issuer:      issuer,
		initialized: true,
	}
	if !principal.Valid() {
		return Principal{}, ErrInvalidPrincipal
	}
	return principal, nil
}

// NewAnonymousPrincipal creates the explicit anonymous security subject.
func NewAnonymousPrincipal() Principal {
	return Principal{kind: PrincipalKindAnonymous, initialized: true}
}

// Kind returns the principal's canonical domain-neutral kind.
func (p Principal) Kind() PrincipalKind {
	if !p.Valid() {
		return ""
	}
	return p.kind
}

// Subject returns the opaque subject identifier, or empty for anonymous.
func (p Principal) Subject() string {
	if !p.Valid() {
		return ""
	}
	return p.subject
}

// Issuer returns the optional opaque issuer reference.
func (p Principal) Issuer() string {
	if !p.Valid() {
		return ""
	}
	return p.issuer
}

// Anonymous reports whether this is the explicit anonymous principal.
func (p Principal) Anonymous() bool {
	return p.Valid() && p.kind == PrincipalKindAnonymous
}

// Valid reports whether the reference has one complete safe shape.
func (p Principal) Valid() bool {
	if !p.initialized || !p.kind.Valid() {
		return false
	}
	if p.kind == PrincipalKindAnonymous {
		return p.subject == "" && p.issuer == ""
	}
	return validPrincipalReference(p.subject, MaximumPrincipalSubjectSize) &&
		(p.issuer == "" || validPrincipalReference(p.issuer, MaximumPrincipalIssuerSize))
}

func validPrincipalReference(value string, maximum int) bool {
	if len(value) == 0 || len(value) > maximum {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		switch {
		case character >= 'a' && character <= 'z',
			character >= 'A' && character <= 'Z',
			character >= '0' && character <= '9':
		case character == '-', character == '.', character == '_', character == '~',
			character == ':', character == '/', character == '?', character == '#',
			character == '[', character == ']', character == '@', character == '!',
			character == '$', character == '&', character == '\'', character == '(',
			character == ')', character == '*', character == '+', character == ',',
			character == ';', character == '=', character == '%':
		default:
			return false
		}
	}
	return true
}
