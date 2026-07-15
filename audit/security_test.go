package audit_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/plystra/kernel/audit"
)

func TestSecurityContextCarriesDistinctPrincipalsAndOpaqueReferences(t *testing.T) {
	t.Parallel()

	caller := testPrincipal(t, audit.PrincipalKindService, "gateway:west", "spiffe://example.test/gateway")
	subject := testPrincipal(t, audit.PrincipalKindUser, "user:01", "https://identity.example.test")
	options := audit.SecurityContextOptions{
		CallerPrincipal:                caller,
		SubjectPrincipal:               subject,
		MemberReference:                "member:west:01",
		AuthorizationBoundaryReference: "tenant:west",
		AuthenticationContextReference: "authn:session:01",
		AuthorizationContextReference:  "authz:decision:01",
	}
	security, err := audit.NewSecurityContext(options)
	if err != nil {
		t.Fatalf("NewSecurityContext: %v", err)
	}
	if !security.Valid() || security.CallerPrincipal() != caller || security.SubjectPrincipal() != subject ||
		security.MemberReference() != options.MemberReference ||
		security.AuthorizationBoundaryReference() != options.AuthorizationBoundaryReference ||
		security.AuthenticationContextReference() != options.AuthenticationContextReference ||
		security.AuthorizationContextReference() != options.AuthorizationContextReference {
		t.Fatalf("SecurityContext = %#v", security)
	}
	if copied := security; copied != security {
		t.Fatalf("SecurityContext copy changed: %#v", copied)
	}
}

func TestSecurityContextDefaultsSubjectToExplicitCaller(t *testing.T) {
	t.Parallel()

	for _, caller := range []audit.Principal{
		audit.NewAnonymousPrincipal(),
		testPrincipal(t, audit.PrincipalKindDevice, "device:01", ""),
	} {
		security, err := audit.NewSecurityContext(audit.SecurityContextOptions{CallerPrincipal: caller})
		if err != nil {
			t.Fatalf("NewSecurityContext: %v", err)
		}
		if !security.Valid() || security.CallerPrincipal() != caller || security.SubjectPrincipal() != caller {
			t.Fatalf("defaulted SecurityContext = %#v", security)
		}
	}
}

func TestSecurityContextRejectsMissingPrincipalsAndUnsafeReferences(t *testing.T) {
	t.Parallel()

	caller := testPrincipal(t, audit.PrincipalKindUser, "user:01", "")
	for _, test := range []struct {
		name    string
		options audit.SecurityContextOptions
	}{
		{name: "missing caller"},
		{name: "unsafe member", options: audit.SecurityContextOptions{CallerPrincipal: caller, MemberReference: "member 01"}},
		{name: "unsafe boundary", options: audit.SecurityContextOptions{CallerPrincipal: caller, AuthorizationBoundaryReference: "tenant\nwest"}},
		{name: "unsafe authentication context", options: audit.SecurityContextOptions{CallerPrincipal: caller, AuthenticationContextReference: `authn\session`}},
		{name: "unsafe authorization context", options: audit.SecurityContextOptions{CallerPrincipal: caller, AuthorizationContextReference: "authz\"decision"}},
		{name: "oversized member", options: audit.SecurityContextOptions{CallerPrincipal: caller, MemberReference: strings.Repeat("m", audit.MaximumSecurityContextReferenceSize+1)}},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			security, err := audit.NewSecurityContext(test.options)
			if !errors.Is(err, audit.ErrInvalidSecurityContext) || security.Valid() {
				t.Fatalf("NewSecurityContext = %#v, %v", security, err)
			}
			if err != nil && err.Error() != audit.ErrInvalidSecurityContext.Error() {
				t.Fatalf("error exposed rejected input: %v", err)
			}
		})
	}
}

func TestZeroSecurityContextFailsClosedAndExposesNoMutableState(t *testing.T) {
	t.Parallel()

	var security audit.SecurityContext
	if security.Valid() || security.CallerPrincipal().Valid() || security.SubjectPrincipal().Valid() ||
		security.MemberReference() != "" || security.AuthorizationBoundaryReference() != "" ||
		security.AuthenticationContextReference() != "" || security.AuthorizationContextReference() != "" {
		t.Fatalf("zero SecurityContext = %#v", security)
	}
	contextType := reflect.TypeFor[audit.SecurityContext]()
	for index := 0; index < contextType.NumField(); index++ {
		if contextType.Field(index).IsExported() {
			t.Fatalf("SecurityContext field %q is exported", contextType.Field(index).Name)
		}
	}
}

func FuzzSecurityContextReferences(f *testing.F) {
	f.Add("member:01", "tenant:west", "authn:session:01", "authz:decision:01")
	f.Add("", "", "", "")
	f.Add("member 01", "tenant\nwest", `authn\session`, "authz\"decision")
	f.Fuzz(func(t *testing.T, member, boundary, authentication, authorization string) {
		caller := testPrincipal(t, audit.PrincipalKindService, "service:01", "")
		options := audit.SecurityContextOptions{
			CallerPrincipal:                caller,
			MemberReference:                member,
			AuthorizationBoundaryReference: boundary,
			AuthenticationContextReference: authentication,
			AuthorizationContextReference:  authorization,
		}
		security, err := audit.NewSecurityContext(options)
		if err != nil {
			if !errors.Is(err, audit.ErrInvalidSecurityContext) || security.Valid() {
				t.Fatalf("rejected SecurityContext = %#v, %v", security, err)
			}
			return
		}
		if !security.Valid() || security.CallerPrincipal() != caller || security.SubjectPrincipal() != caller ||
			security.MemberReference() != member || security.AuthorizationBoundaryReference() != boundary ||
			security.AuthenticationContextReference() != authentication || security.AuthorizationContextReference() != authorization {
			t.Fatalf("accepted SecurityContext = %#v", security)
		}
	})
}

func testPrincipal(t *testing.T, kind audit.PrincipalKind, subject, issuer string) audit.Principal {
	t.Helper()
	principal, err := audit.NewPrincipal(kind, subject, issuer)
	if err != nil {
		t.Fatalf("NewPrincipal: %v", err)
	}
	return principal
}
