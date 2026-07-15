package audit_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/kernel/audit"
)

func TestSubjectContextSupportsExplicitOptionalReferences(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		subject string
		tenant  string
	}{
		{name: "anonymous"},
		{name: "subject only", subject: "user:01"},
		{name: "tenant only", tenant: "space.acme-01"},
		{name: "subject and tenant", subject: "member_01", tenant: "tenant:west_1"},
	} {
		context, err := audit.NewSubjectContext(test.subject, test.tenant)
		if err != nil {
			t.Fatalf("%s NewSubjectContext: %v", test.name, err)
		}
		if !context.Valid() || context.SubjectIdentity() != test.subject || context.TenantIdentity() != test.tenant {
			t.Fatalf("%s context = %#v", test.name, context)
		}
		copied := context
		if copied != context {
			t.Fatalf("%s copy changed context", test.name)
		}
	}
}

func TestSubjectContextAcceptsMaximumSafeReferences(t *testing.T) {
	t.Parallel()

	reference := strings.Repeat("a", audit.MaximumSubjectReferenceSize)
	context, err := audit.NewSubjectContext(reference, reference)
	if err != nil || !context.Valid() || context.SubjectIdentity() != reference || context.TenantIdentity() != reference {
		t.Fatalf("maximum context = %#v, %v", context, err)
	}
}

func TestSubjectContextRejectsUnsafeReferences(t *testing.T) {
	t.Parallel()

	for _, value := range []string{
		" subject",
		"subject ",
		"-subject",
		"_subject",
		".subject",
		":subject",
		"subject/value",
		"subject@host",
		"subject\nforged",
		"秘密",
		strings.Repeat("a", audit.MaximumSubjectReferenceSize+1),
	} {
		for _, references := range [][2]string{{value, ""}, {"", value}} {
			context, err := audit.NewSubjectContext(references[0], references[1])
			if !errors.Is(err, audit.ErrInvalidSubjectContext) {
				t.Fatalf("NewSubjectContext(%q, %q) error = %v", references[0], references[1], err)
			}
			if context.Valid() || context.SubjectIdentity() != "" || context.TenantIdentity() != "" {
				t.Fatalf("rejected context = %#v", context)
			}
		}
	}
}

func TestZeroSubjectContextIsNotAnonymous(t *testing.T) {
	t.Parallel()

	var context audit.SubjectContext
	if context.Valid() || context.SubjectIdentity() != "" || context.TenantIdentity() != "" {
		t.Fatalf("zero context = %#v", context)
	}
}

func TestSubjectContextExposesNoMutableState(t *testing.T) {
	t.Parallel()

	contextType := reflect.TypeFor[audit.SubjectContext]()
	for index := range contextType.NumField() {
		if field := contextType.Field(index); field.IsExported() {
			t.Fatalf("SubjectContext field %q exposes mutable state", field.Name)
		}
	}
}

func FuzzSubjectContext(f *testing.F) {
	f.Add("user:01", "tenant:west")
	f.Add("", "")
	f.Add("bad subject", "tenant")

	f.Fuzz(func(t *testing.T, subject, tenant string) {
		context, err := audit.NewSubjectContext(subject, tenant)
		if err != nil {
			if !errors.Is(err, audit.ErrInvalidSubjectContext) || context.Valid() {
				t.Fatalf("NewSubjectContext(%q, %q) = %#v, %v", subject, tenant, context, err)
			}
			return
		}
		if !context.Valid() || context.SubjectIdentity() != subject || context.TenantIdentity() != tenant {
			t.Fatalf("NewSubjectContext(%q, %q) = %#v", subject, tenant, context)
		}
	})
}
