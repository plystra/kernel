package audit

import "errors"

// MaximumSubjectReferenceSize bounds each opaque audited subject reference.
const MaximumSubjectReferenceSize = 128

// ErrInvalidSubjectContext reports an unsafe subject or tenant reference.
var ErrInvalidSubjectContext = errors.New("invalid runtime subject context")

// SubjectContext carries optional authenticated subject and tenant or space
// references without defining AuthN or AuthZ domain types in the Kernel. Even
// an anonymous context is explicitly constructed and distinguishable from the
// invalid zero value.
type SubjectContext struct {
	subjectIdentity string
	tenantIdentity  string
	initialized     bool
}

// NewSubjectContext validates and records optional opaque subject and tenant
// or space references. Credentials, tokens, claims, and policy details do not
// belong in these identifiers.
func NewSubjectContext(subjectIdentity, tenantIdentity string) (SubjectContext, error) {
	context := SubjectContext{
		subjectIdentity: subjectIdentity,
		tenantIdentity:  tenantIdentity,
		initialized:     true,
	}
	if !context.Valid() {
		return SubjectContext{}, ErrInvalidSubjectContext
	}
	return context, nil
}

// SubjectIdentity returns the optional authenticated subject reference.
func (c SubjectContext) SubjectIdentity() string {
	if !c.Valid() {
		return ""
	}
	return c.subjectIdentity
}

// TenantIdentity returns the optional tenant or authorization-space reference.
func (c SubjectContext) TenantIdentity() string {
	if !c.Valid() {
		return ""
	}
	return c.tenantIdentity
}

// Valid reports whether this is an explicitly constructed safe context.
func (c SubjectContext) Valid() bool {
	return c.initialized &&
		(c.subjectIdentity == "" || validSubjectReference(c.subjectIdentity)) &&
		(c.tenantIdentity == "" || validSubjectReference(c.tenantIdentity))
}

func validSubjectReference(value string) bool {
	if len(value) == 0 || len(value) > MaximumSubjectReferenceSize {
		return false
	}
	for index := 0; index < len(value); index++ {
		character := value[index]
		switch {
		case character >= 'a' && character <= 'z',
			character >= 'A' && character <= 'Z',
			character >= '0' && character <= '9':
		case index != 0 && (character == '.' || character == '_' || character == ':' || character == '-'):
		default:
			return false
		}
	}
	return true
}
