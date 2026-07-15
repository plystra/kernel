package invocation

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/plystra/kernel/audit"
	"github.com/plystra/kernel/capability"
)

func TestAuthorizationRequestCarriesOnlyGovernedFacts(t *testing.T) {
	t.Parallel()

	scope := testHandleScope(t)
	subject, err := audit.NewSubjectContext("subject:operator", "tenant:west")
	if err != nil {
		t.Fatalf("NewSubjectContext: %v", err)
	}
	root, err := scope.NewRootContext(context.Background(), subject)
	if err != nil {
		t.Fatalf("NewRootContext: %v", err)
	}
	invocationID := testInvocationID(t, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	entered, cleanup, err := enterInvocationContext(root, invocationID, time.Minute)
	if err != nil {
		t.Fatalf("enterInvocationContext: %v", err)
	}
	defer cleanup()
	identifier := mustCapabilityID(t, "records.read/v1")

	request, err := newAuthorizationRequest(entered, scope, identifier)
	if err != nil {
		t.Fatalf("newAuthorizationRequest: %v", err)
	}
	current, _ := Current(entered)
	if !request.Valid() || request.Caller() != scope.caller || request.Capability() != identifier {
		t.Fatalf("authorization request = %#v", request)
	}
	if request.RequestID() != current.RequestID() || request.TraceID() != current.TraceID() || request.InvocationID() != invocationID || request.ParentInvocationID().Valid() {
		t.Fatalf("authorization identities = %#v", request)
	}
	if request.SubjectIdentity() != "subject:operator" || request.TenantIdentity() != "tenant:west" {
		t.Fatalf("authorization subject = %#v", request)
	}

	var zero AuthorizationRequest
	if zero.Valid() || zero.Caller().Valid() || zero.Capability().String() != "" || zero.RequestID().Valid() || zero.TraceID().Valid() || zero.InvocationID().Valid() || zero.ParentInvocationID().Valid() || zero.SubjectIdentity() != "" || zero.TenantIdentity() != "" {
		t.Fatalf("zero authorization request = %#v", zero)
	}
}

func TestAuthorizationRequestRejectsUngovernedInputs(t *testing.T) {
	t.Parallel()

	scope := testHandleScope(t)
	subject, err := audit.NewSubjectContext("", "")
	if err != nil {
		t.Fatalf("NewSubjectContext: %v", err)
	}
	root, err := scope.NewRootContext(context.Background(), subject)
	if err != nil {
		t.Fatalf("NewRootContext: %v", err)
	}
	entered, cleanup, err := enterInvocationContext(root, testInvocationID(t, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"), time.Minute)
	if err != nil {
		t.Fatalf("enterInvocationContext: %v", err)
	}
	defer cleanup()
	identifier := mustCapabilityID(t, "records.read/v1")
	corrupt := context.WithValue(entered, runtimeFrameKey{}, runtimeFrame{})

	for _, test := range []struct {
		name       string
		ctx        context.Context
		scope      Scope
		identifier bool
	}{
		{name: "nil context", scope: scope, identifier: true},
		{name: "missing frame", ctx: context.Background(), scope: scope, identifier: true},
		{name: "root frame", ctx: root, scope: scope, identifier: true},
		{name: "corrupt frame", ctx: corrupt, scope: scope, identifier: true},
		{name: "zero scope", ctx: entered, identifier: true},
		{name: "zero capability", ctx: entered, scope: scope},
	} {
		capabilityID := identifier
		if !test.identifier {
			capabilityID = capability.Identifier{}
		}
		request, err := newAuthorizationRequest(test.ctx, test.scope, capabilityID)
		if !errors.Is(err, ErrInvalidAuthorization) || request.Valid() {
			t.Fatalf("%s newAuthorizationRequest = %#v, %v", test.name, request, err)
		}
	}
}

func TestAuthorizationDecisionsRequireExplicitSafeOutcomes(t *testing.T) {
	t.Parallel()

	allowed := NewAllowedAuthorization()
	if !allowed.Valid() || !allowed.Allowed() || allowed.DenialCode() != "" {
		t.Fatalf("allowed decision = %#v", allowed)
	}
	denied, err := NewDeniedAuthorization("authorization.tenant_denied")
	if err != nil || !denied.Valid() || denied.Allowed() || denied.DenialCode() != "authorization.tenant_denied" {
		t.Fatalf("denied decision = %#v, %v", denied, err)
	}
	for _, code := range []string{"", "Denied", "authorization-denied", ".denied", "denied.", "denied..policy", "denied policy"} {
		decision, err := NewDeniedAuthorization(code)
		if !errors.Is(err, ErrInvalidAuthorization) || decision.Valid() {
			t.Fatalf("NewDeniedAuthorization(%q) = %#v, %v", code, decision, err)
		}
	}
	var zero AuthorizationDecision
	if zero.Valid() || zero.Allowed() || zero.DenialCode() != "" {
		t.Fatalf("zero decision = %#v", zero)
	}
}

func TestCapabilityAuthorizerFailsClosed(t *testing.T) {
	t.Parallel()

	ctx, request := testAuthorizationRequest(t)
	allowed := NewAllowedAuthorization()
	denied, err := NewDeniedAuthorization("authorization.policy_denied")
	if err != nil {
		t.Fatalf("NewDeniedAuthorization: %v", err)
	}
	sensitive := errors.New("database password was rejected")

	for _, test := range []struct {
		name       string
		authorizer Authorizer
		want       AuthorizationDecision
		wantErr    error
	}{
		{name: "allow", authorizer: func(received context.Context, candidate AuthorizationRequest) (AuthorizationDecision, error) {
			if received != ctx || candidate != request {
				t.Fatalf("authorizer input = %#v / %#v", received, candidate)
			}
			return allowed, nil
		}, want: allowed},
		{name: "deny", authorizer: func(context.Context, AuthorizationRequest) (AuthorizationDecision, error) {
			return denied, nil
		}, want: denied},
		{name: "zero decision", authorizer: func(context.Context, AuthorizationRequest) (AuthorizationDecision, error) {
			return AuthorizationDecision{}, nil
		}, wantErr: ErrAuthorizationFailed},
		{name: "malformed decision", authorizer: func(context.Context, AuthorizationRequest) (AuthorizationDecision, error) {
			return AuthorizationDecision{outcome: authorizationAllowed, denialCode: "authorization.contradictory"}, nil
		}, wantErr: ErrAuthorizationFailed},
		{name: "allow with error", authorizer: func(context.Context, AuthorizationRequest) (AuthorizationDecision, error) {
			return allowed, sensitive
		}, wantErr: ErrAuthorizationFailed},
		{name: "panic", authorizer: func(context.Context, AuthorizationRequest) (AuthorizationDecision, error) {
			panic("sensitive policy state")
		}, wantErr: ErrAuthorizationFailed},
	} {
		decision, err := authorizeCapability(ctx, test.authorizer, request)
		if !errors.Is(err, test.wantErr) || decision != test.want {
			t.Fatalf("%s authorizeCapability = %#v, %v", test.name, decision, err)
		}
		if err != nil && (err.Error() != test.wantErr.Error() || errors.Is(err, sensitive)) {
			t.Fatalf("%s leaked authorizer failure: %v", test.name, err)
		}
	}

	for _, test := range []struct {
		name       string
		ctx        context.Context
		authorizer Authorizer
		request    AuthorizationRequest
	}{
		{name: "nil context", authorizer: testAuthorizationAllow, request: request},
		{name: "nil authorizer", ctx: ctx, request: request},
		{name: "invalid request", ctx: ctx, authorizer: testAuthorizationAllow},
	} {
		decision, err := authorizeCapability(test.ctx, test.authorizer, test.request)
		if !errors.Is(err, ErrInvalidAuthorization) || decision.Valid() {
			t.Fatalf("%s authorizeCapability = %#v, %v", test.name, decision, err)
		}
	}
}

func testAuthorizationRequest(t *testing.T) (context.Context, AuthorizationRequest) {
	t.Helper()
	scope := testHandleScope(t)
	subject, err := audit.NewSubjectContext("", "")
	if err != nil {
		t.Fatalf("NewSubjectContext: %v", err)
	}
	root, err := scope.NewRootContext(context.Background(), subject)
	if err != nil {
		t.Fatalf("NewRootContext: %v", err)
	}
	entered, cleanup, err := enterInvocationContext(root, testInvocationID(t, "cccccccccccccccccccccccccccccccc"), time.Minute)
	if err != nil {
		t.Fatalf("enterInvocationContext: %v", err)
	}
	t.Cleanup(cleanup)
	request, err := newAuthorizationRequest(entered, scope, mustCapabilityID(t, "records.read/v1"))
	if err != nil {
		t.Fatalf("newAuthorizationRequest: %v", err)
	}
	return entered, request
}

func FuzzDeniedAuthorization(f *testing.F) {
	f.Add("authorization.policy_denied")
	f.Add("")
	f.Add("Denied")
	f.Fuzz(func(t *testing.T, code string) {
		decision, err := NewDeniedAuthorization(code)
		if err != nil {
			if !errors.Is(err, ErrInvalidAuthorization) || decision.Valid() {
				t.Fatalf("rejected decision = %#v, %v", decision, err)
			}
			return
		}
		if !decision.Valid() || decision.Allowed() || decision.DenialCode() != code {
			t.Fatalf("accepted decision = %#v", decision)
		}
	})
}
