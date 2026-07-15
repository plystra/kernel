package capability_test

import (
	"errors"
	"testing"

	"github.com/plystra/kernel/capability"
)

func TestParseIdentifier(t *testing.T) {
	t.Parallel()

	tests := []struct {
		value     string
		wantName  string
		wantMajor uint64
	}{
		{value: "email.send/v1", wantName: "email.send", wantMajor: 1},
		{value: "workspace.invite-member/v12", wantName: "workspace.invite-member", wantMajor: 12},
		{value: "authn.login.password/v1", wantName: "authn.login.password", wantMajor: 1},
		{value: "authn.login.oidc.complete/v1", wantName: "authn.login.oidc.complete", wantMajor: 1},
		{value: "authn.passkey.challenge.create/v1", wantName: "authn.passkey.challenge.create", wantMajor: 1},
		{value: "workflow.retry--now-/v2", wantName: "workflow.retry--now-", wantMajor: 2},
		{value: "storage.object.put/v18446744073709551615", wantName: "storage.object.put", wantMajor: ^uint64(0)},
	}

	for _, test := range tests {
		t.Run(test.value, func(t *testing.T) {
			t.Parallel()

			identifier, err := capability.ParseIdentifier(test.value)
			if err != nil {
				t.Fatalf("ParseIdentifier(%q): %v", test.value, err)
			}
			if identifier.Name() != test.wantName {
				t.Fatalf("Name() = %q, want %q", identifier.Name(), test.wantName)
			}
			if identifier.Major() != test.wantMajor {
				t.Fatalf("Major() = %d, want %d", identifier.Major(), test.wantMajor)
			}
			if identifier.String() != test.value {
				t.Fatalf("String() = %q, want %q", identifier.String(), test.value)
			}
		})
	}
}

func TestParseIdentifierRejectsInvalidValues(t *testing.T) {
	t.Parallel()

	invalid := []string{
		"",
		"email/v1",
		"Email.send/v1",
		"email.Send/v1",
		"email.send/V1",
		"email.send/v0",
		"email.send/v01",
		"email.send/v-1",
		"email.send/v18446744073709551616",
		"email_send/v1",
		"email..send/v1",
		"email.-send/v1",
		"email.1send/v1",
		"email.send_/v1",
		"email.send/v1/extra",
		" email.send/v1",
		"email.send/v1 ",
		"邮件.send/v1",
	}

	for _, value := range invalid {
		t.Run(value, func(t *testing.T) {
			t.Parallel()

			identifier, err := capability.ParseIdentifier(value)
			if !errors.Is(err, capability.ErrInvalidIdentifier) {
				t.Fatalf("ParseIdentifier(%q) error = %v, want ErrInvalidIdentifier", value, err)
			}
			if identifier.String() != "" {
				t.Fatalf("invalid identifier String() = %q, want empty", identifier.String())
			}
		})
	}
}

func TestNewIdentifier(t *testing.T) {
	t.Parallel()

	identifier, err := capability.NewIdentifier("account.register", 2)
	if err != nil {
		t.Fatalf("NewIdentifier: %v", err)
	}
	if got := identifier.String(); got != "account.register/v2" {
		t.Fatalf("String() = %q, want account.register/v2", got)
	}
}

func TestNewIdentifierRejectsInvalidParts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		major uint64
	}{
		{name: "authorization", major: 1},
		{name: "authorization.check", major: 0},
	}
	for _, test := range tests {
		_, err := capability.NewIdentifier(test.name, test.major)
		if !errors.Is(err, capability.ErrInvalidIdentifier) {
			t.Fatalf("NewIdentifier(%q, %d) error = %v, want ErrInvalidIdentifier", test.name, test.major, err)
		}
	}
}

func TestZeroIdentifierHasEmptyAccessors(t *testing.T) {
	t.Parallel()

	var identifier capability.Identifier
	if identifier.Name() != "" || identifier.Major() != 0 || identifier.String() != "" {
		t.Fatalf("zero Identifier = name %q, major %d, string %q", identifier.Name(), identifier.Major(), identifier.String())
	}
}

func FuzzParseIdentifier(f *testing.F) {
	for _, seed := range []string{
		"email.send/v1",
		"workspace.invite-member/v12",
		"authn.login.oidc.complete/v1",
		"workflow.retry--now-/v2",
		"bad",
		"email.send/v0",
		"邮件.send/v1",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, value string) {
		identifier, err := capability.ParseIdentifier(value)
		if err != nil {
			if !errors.Is(err, capability.ErrInvalidIdentifier) {
				t.Fatalf("ParseIdentifier(%q) returned unexpected error: %v", value, err)
			}
			return
		}

		if got := identifier.String(); got != value {
			t.Fatalf("round trip = %q, want %q", got, value)
		}
		reparsed, err := capability.ParseIdentifier(identifier.String())
		if err != nil {
			t.Fatalf("reparse canonical identifier: %v", err)
		}
		if reparsed != identifier {
			t.Fatalf("reparsed identifier = %#v, want %#v", reparsed, identifier)
		}
	})
}
