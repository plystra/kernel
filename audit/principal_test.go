package audit_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/plystra/kernel/audit"
)

func TestPrincipalSupportsStandardAndExtensibleKinds(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		kind    audit.PrincipalKind
		subject string
		issuer  string
	}{
		{name: "user", kind: audit.PrincipalKindUser, subject: "user:01", issuer: "https://identity.example.com/tenant"},
		{name: "service", kind: audit.PrincipalKindService, subject: "billing-worker"},
		{name: "plugin", kind: audit.PrincipalKindPlugin, subject: "acme.email.smtp", issuer: "plystra://runtime"},
		{name: "system", kind: audit.PrincipalKindSystem, subject: "scheduler"},
		{name: "device", kind: audit.PrincipalKindDevice, subject: "urn:device:01"},
		{name: "extensible", kind: audit.PrincipalKind("acme.workload"), subject: "queue/consumer#3"},
		{name: "maximum values", kind: audit.PrincipalKind(strings.Repeat("a", audit.MaximumPrincipalKindSize)), subject: strings.Repeat("s", audit.MaximumPrincipalSubjectSize), issuer: strings.Repeat("i", audit.MaximumPrincipalIssuerSize)},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			principal, err := audit.NewPrincipal(test.kind, test.subject, test.issuer)
			if err != nil {
				t.Fatalf("NewPrincipal: %v", err)
			}
			if !principal.Valid() || principal.Anonymous() || principal.Kind() != test.kind || principal.Kind().String() != string(test.kind) || principal.Subject() != test.subject || principal.Issuer() != test.issuer {
				t.Fatalf("Principal = %#v", principal)
			}
			if copied := principal; copied != principal {
				t.Fatalf("Principal copy changed: %#v", copied)
			}
		})
	}
}

func TestPrincipalRequiresExplicitAnonymousShape(t *testing.T) {
	t.Parallel()

	principal := audit.NewAnonymousPrincipal()
	if !principal.Valid() || !principal.Anonymous() || principal.Kind() != audit.PrincipalKindAnonymous || principal.Subject() != "" || principal.Issuer() != "" {
		t.Fatalf("anonymous Principal = %#v", principal)
	}
	constructed, err := audit.NewPrincipal(audit.PrincipalKindAnonymous, "", "")
	if err != nil || constructed != principal {
		t.Fatalf("constructed anonymous Principal = %#v, %v", constructed, err)
	}
	var zero audit.Principal
	if zero.Valid() || zero.Anonymous() || zero.Kind().Valid() || zero.Subject() != "" || zero.Issuer() != "" {
		t.Fatalf("zero Principal = %#v", zero)
	}
}

func TestPrincipalRejectsUnsafeOrIncompleteReferences(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name    string
		kind    audit.PrincipalKind
		subject string
		issuer  string
	}{
		{name: "missing kind", subject: "subject"},
		{name: "uppercase kind", kind: "User", subject: "subject"},
		{name: "numeric kind prefix", kind: "2fa-device", subject: "subject"},
		{name: "kind leading separator", kind: ".service", subject: "subject"},
		{name: "kind trailing separator", kind: "service-", subject: "subject"},
		{name: "kind repeated separator", kind: "acme..service", subject: "subject"},
		{name: "oversized kind", kind: audit.PrincipalKind(strings.Repeat("a", audit.MaximumPrincipalKindSize+1)), subject: "subject"},
		{name: "missing subject", kind: audit.PrincipalKindUser},
		{name: "anonymous subject", kind: audit.PrincipalKindAnonymous, subject: "subject"},
		{name: "anonymous issuer", kind: audit.PrincipalKindAnonymous, issuer: "issuer"},
		{name: "subject whitespace", kind: audit.PrincipalKindUser, subject: "user 01"},
		{name: "subject newline", kind: audit.PrincipalKindUser, subject: "user\n01"},
		{name: "subject quote", kind: audit.PrincipalKindUser, subject: "user\"01"},
		{name: "subject backslash", kind: audit.PrincipalKindUser, subject: `user\01`},
		{name: "subject unicode", kind: audit.PrincipalKindUser, subject: "\u7528\u6237"},
		{name: "oversized subject", kind: audit.PrincipalKindUser, subject: strings.Repeat("s", audit.MaximumPrincipalSubjectSize+1)},
		{name: "issuer whitespace", kind: audit.PrincipalKindUser, subject: "subject", issuer: "issuer value"},
		{name: "oversized issuer", kind: audit.PrincipalKindUser, subject: "subject", issuer: strings.Repeat("i", audit.MaximumPrincipalIssuerSize+1)},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			principal, err := audit.NewPrincipal(test.kind, test.subject, test.issuer)
			if !errors.Is(err, audit.ErrInvalidPrincipal) || principal.Valid() {
				t.Fatalf("NewPrincipal = %#v, %v", principal, err)
			}
			if err != nil && err.Error() != audit.ErrInvalidPrincipal.Error() {
				t.Fatalf("error exposed rejected input: %v", err)
			}
		})
	}
}

func FuzzPrincipal(f *testing.F) {
	f.Add("user", "user:01", "https://identity.example.com")
	f.Add("anonymous", "", "")
	f.Add("acme.workload", "queue/consumer#3", "")
	f.Add("User", "user 01", "issuer\nvalue")
	f.Fuzz(func(t *testing.T, kind, subject, issuer string) {
		principal, err := audit.NewPrincipal(audit.PrincipalKind(kind), subject, issuer)
		if err != nil {
			if !errors.Is(err, audit.ErrInvalidPrincipal) || principal.Valid() {
				t.Fatalf("rejected Principal = %#v, %v", principal, err)
			}
			return
		}
		if !principal.Valid() || principal.Kind().String() != kind || principal.Subject() != subject || principal.Issuer() != issuer || principal.Anonymous() != (kind == "anonymous") {
			t.Fatalf("accepted Principal = %#v", principal)
		}
	})
}
