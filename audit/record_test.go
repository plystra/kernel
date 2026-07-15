package audit_test

import (
	"crypto/sha256"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/plystra/kernel/audit"
	"github.com/plystra/kernel/capability"
	"github.com/plystra/kernel/plugin"
)

func TestInvocationRecordPreservesCompleteTerminalEnvelope(t *testing.T) {
	t.Parallel()

	options := validInvocationRecordOptions(t)
	options.ParentInvocationID = testInvocationRecordID(t, "44444444444444444444444444444444")
	options.ExecutionClass = audit.ExecutionRemote
	subject := testPrincipal(t, audit.PrincipalKindUser, "user:01", "https://identity.example.test")
	security, err := audit.NewSecurityContext(audit.SecurityContextOptions{
		CallerPrincipal:                options.SecurityContext.CallerPrincipal(),
		SubjectPrincipal:               subject,
		MemberReference:                "member:west:01",
		AuthorizationBoundaryReference: "tenant:west",
		AuthenticationContextReference: "authn:session:01",
		AuthorizationContextReference:  "authz:decision:01",
	})
	if err != nil {
		t.Fatalf("NewSecurityContext: %v", err)
	}
	options.SecurityContext = security
	options.StartedAt = time.Date(2026, time.July, 15, 12, 34, 56, 123456789, time.FixedZone("test", 8*60*60))
	options.CompletedAt = options.StartedAt.Add(1250 * time.Millisecond)
	options.Duration = 1200 * time.Millisecond
	outcome, err := audit.NewErrorOutcome(audit.ErrorDenied, "authorization.policy_denied")
	if err != nil {
		t.Fatalf("NewErrorOutcome: %v", err)
	}
	options.Outcome = outcome

	record, err := audit.NewInvocationRecord(options)
	if err != nil {
		t.Fatalf("NewInvocationRecord: %v", err)
	}
	if !record.Valid() || record.RuntimeCaller() != options.RuntimeCaller || record.Capability() != options.Capability ||
		record.CapabilitySchemaDigest() != options.CapabilitySchemaDigest || record.ProviderKind() != options.ProviderKind ||
		record.ProviderPluginID() != options.ProviderPluginID || record.ProviderBuild() != options.ProviderBuild ||
		record.SecurityContext() != options.SecurityContext || record.RequestID() != options.RequestID ||
		record.TraceID() != options.TraceID || record.InvocationID() != options.InvocationID ||
		record.ParentInvocationID() != options.ParentInvocationID || record.ExecutionClass() != options.ExecutionClass ||
		record.StartedAt() != options.StartedAt.UTC() || record.CompletedAt() != options.CompletedAt.UTC() ||
		record.Duration() != options.Duration || record.Outcome() != outcome {
		t.Fatalf("InvocationRecord = %#v", record)
	}
	if record.StartedAt().Location() != time.UTC || record.CompletedAt().Location() != time.UTC {
		t.Fatalf("record times are not UTC: %v, %v", record.StartedAt(), record.CompletedAt())
	}
	if copied := record; copied != record {
		t.Fatalf("InvocationRecord copy changed: %#v", copied)
	}
}

func TestInvocationRecordSupportsKernelProviderAndAnonymousPrincipal(t *testing.T) {
	t.Parallel()

	options := validInvocationRecordOptions(t)
	options.RuntimeCaller = audit.NewKernelCallerIdentity()
	options.ProviderKind = audit.ProviderKindKernel
	options.ProviderPluginID = plugin.ID{}
	options.SecurityContext = testSecurityContext(t, audit.NewAnonymousPrincipal())
	options.ParentInvocationID = audit.InvocationID{}
	options.ExecutionClass = audit.ExecutionLocal
	options.Outcome = audit.NewSucceededOutcome()

	record, err := audit.NewInvocationRecord(options)
	if err != nil {
		t.Fatalf("NewInvocationRecord: %v", err)
	}
	if !record.Valid() || record.ProviderKind() != audit.ProviderKindKernel || record.ProviderPluginID().String() != "" ||
		record.ParentInvocationID().Valid() || !record.SecurityContext().CallerPrincipal().Anonymous() ||
		record.Outcome().Status() != audit.OutcomeSucceeded {
		t.Fatalf("Kernel InvocationRecord = %#v", record)
	}
}

func TestInvocationRecordRejectsIncompleteOrContradictoryFacts(t *testing.T) {
	t.Parallel()

	valid := validInvocationRecordOptions(t)
	for _, test := range []struct {
		name   string
		mutate func(*audit.InvocationRecordOptions)
	}{
		{name: "missing runtime caller", mutate: func(options *audit.InvocationRecordOptions) { options.RuntimeCaller = audit.CallerIdentity{} }},
		{name: "missing capability", mutate: func(options *audit.InvocationRecordOptions) { options.Capability = capability.Identifier{} }},
		{name: "missing schema digest", mutate: func(options *audit.InvocationRecordOptions) { options.CapabilitySchemaDigest = [sha256.Size]byte{} }},
		{name: "unknown provider kind", mutate: func(options *audit.InvocationRecordOptions) { options.ProviderKind = "external" }},
		{name: "Kernel provider with Plugin ID", mutate: func(options *audit.InvocationRecordOptions) { options.ProviderKind = audit.ProviderKindKernel }},
		{name: "plugin provider without Plugin ID", mutate: func(options *audit.InvocationRecordOptions) { options.ProviderPluginID = plugin.ID{} }},
		{name: "missing provider build", mutate: func(options *audit.InvocationRecordOptions) { options.ProviderBuild = audit.ModuleBuild{} }},
		{name: "missing security context", mutate: func(options *audit.InvocationRecordOptions) { options.SecurityContext = audit.SecurityContext{} }},
		{name: "missing request ID", mutate: func(options *audit.InvocationRecordOptions) { options.RequestID = audit.RequestID{} }},
		{name: "missing trace ID", mutate: func(options *audit.InvocationRecordOptions) { options.TraceID = audit.TraceID{} }},
		{name: "missing invocation ID", mutate: func(options *audit.InvocationRecordOptions) { options.InvocationID = audit.InvocationID{} }},
		{name: "self parent", mutate: func(options *audit.InvocationRecordOptions) { options.ParentInvocationID = options.InvocationID }},
		{name: "unknown execution class", mutate: func(options *audit.InvocationRecordOptions) { options.ExecutionClass = "loopback" }},
		{name: "missing start time", mutate: func(options *audit.InvocationRecordOptions) { options.StartedAt = time.Time{} }},
		{name: "missing completion time", mutate: func(options *audit.InvocationRecordOptions) { options.CompletedAt = time.Time{} }},
		{name: "unrepresentable start year", mutate: func(options *audit.InvocationRecordOptions) {
			options.StartedAt = time.Date(10000, time.January, 1, 0, 0, 0, 0, time.UTC)
		}},
		{name: "completion before start", mutate: func(options *audit.InvocationRecordOptions) {
			options.CompletedAt = options.StartedAt.Add(-time.Nanosecond)
		}},
		{name: "negative duration", mutate: func(options *audit.InvocationRecordOptions) { options.Duration = -time.Nanosecond }},
		{name: "missing outcome", mutate: func(options *audit.InvocationRecordOptions) { options.Outcome = audit.Outcome{} }},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			options := valid
			test.mutate(&options)
			record, err := audit.NewInvocationRecord(options)
			if !errors.Is(err, audit.ErrInvalidInvocationRecord) || record.Valid() {
				t.Fatalf("NewInvocationRecord = %#v, %v", record, err)
			}
			if err != nil && err.Error() != audit.ErrInvalidInvocationRecord.Error() {
				t.Fatalf("error exposed rejected input: %v", err)
			}
		})
	}
}

func TestZeroInvocationRecordAndClassificationsFailClosed(t *testing.T) {
	t.Parallel()

	var record audit.InvocationRecord
	if record.Valid() || record.RuntimeCaller().Valid() || record.Capability().String() != "" ||
		record.CapabilitySchemaDigest() != [sha256.Size]byte{} || record.ProviderKind().Valid() ||
		record.ProviderPluginID().String() != "" || record.ProviderBuild().Valid() || record.SecurityContext().Valid() ||
		record.RequestID().Valid() || record.TraceID().Valid() || record.InvocationID().Valid() ||
		record.ParentInvocationID().Valid() || record.ExecutionClass().Valid() || !record.StartedAt().IsZero() ||
		!record.CompletedAt().IsZero() || record.Duration() != 0 || record.Outcome().Valid() {
		t.Fatalf("zero InvocationRecord = %#v", record)
	}
	for _, kind := range []audit.ProviderKind{"", "PLUGIN", "external"} {
		if kind.Valid() || kind.String() != "" {
			t.Fatalf("invalid ProviderKind %q accepted", kind)
		}
	}
	for _, class := range []audit.ExecutionClass{"", "LOCAL", "external_inbound"} {
		if class.Valid() || class.String() != "" {
			t.Fatalf("invalid ExecutionClass %q accepted", class)
		}
	}
	recordType := reflect.TypeFor[audit.InvocationRecord]()
	for index := 0; index < recordType.NumField(); index++ {
		if recordType.Field(index).IsExported() {
			t.Fatalf("InvocationRecord field %q is exported", recordType.Field(index).Name)
		}
	}
}

func FuzzInvocationRecordTerminalFacts(f *testing.F) {
	f.Add("local", "plugin", int64(time.Millisecond), int64(time.Millisecond), false)
	f.Add("remote", "kernel", int64(0), int64(0), false)
	f.Add("external", "plugin", int64(-1), int64(-1), true)
	f.Fuzz(func(t *testing.T, execution, providerKind string, durationNanoseconds, completionDeltaNanoseconds int64, selfParent bool) {
		options := validInvocationRecordOptions(t)
		options.ExecutionClass = audit.ExecutionClass(execution)
		options.ProviderKind = audit.ProviderKind(providerKind)
		if options.ProviderKind != audit.ProviderKindPlugin {
			options.ProviderPluginID = plugin.ID{}
		}
		options.Duration = time.Duration(durationNanoseconds)
		options.CompletedAt = options.StartedAt.Add(time.Duration(completionDeltaNanoseconds))
		if selfParent {
			options.ParentInvocationID = options.InvocationID
		}

		record, err := audit.NewInvocationRecord(options)
		shouldBeValid := options.ExecutionClass.Valid() && options.ProviderKind.Valid() &&
			options.Duration >= 0 && !options.CompletedAt.Before(options.StartedAt) && !selfParent
		if !shouldBeValid {
			if !errors.Is(err, audit.ErrInvalidInvocationRecord) || record.Valid() {
				t.Fatalf("rejected InvocationRecord = %#v, %v", record, err)
			}
			return
		}
		if err != nil || !record.Valid() || record.ExecutionClass() != options.ExecutionClass ||
			record.ProviderKind() != options.ProviderKind || record.Duration() != options.Duration {
			t.Fatalf("accepted InvocationRecord = %#v, %v", record, err)
		}
	})
}

func validInvocationRecordOptions(t *testing.T) audit.InvocationRecordOptions {
	t.Helper()
	callerPluginID, err := plugin.ParseID("acme.checkout.caller")
	if err != nil {
		t.Fatalf("ParseID caller: %v", err)
	}
	runtimeCaller, err := audit.NewPluginCallerIdentity(callerPluginID)
	if err != nil {
		t.Fatalf("NewPluginCallerIdentity: %v", err)
	}
	providerPluginID, err := plugin.ParseID("acme.email.provider")
	if err != nil {
		t.Fatalf("ParseID provider: %v", err)
	}
	identifier, err := capability.ParseIdentifier("email.send/v1")
	if err != nil {
		t.Fatalf("ParseIdentifier: %v", err)
	}
	providerBuild, err := audit.NewModuleBuild("github.com/acme/email", "v1.4.2", "")
	if err != nil {
		t.Fatalf("NewModuleBuild: %v", err)
	}
	callerPrincipal := testPrincipal(t, audit.PrincipalKindService, "gateway:west", "spiffe://example.test/gateway")
	security := testSecurityContext(t, callerPrincipal)
	startedAt := time.Date(2026, time.July, 15, 10, 0, 0, 0, time.UTC)
	return audit.InvocationRecordOptions{
		RuntimeCaller:          runtimeCaller,
		Capability:             identifier,
		CapabilitySchemaDigest: sha256.Sum256([]byte("canonical email.send/v1 schema")),
		ProviderKind:           audit.ProviderKindPlugin,
		ProviderPluginID:       providerPluginID,
		ProviderBuild:          providerBuild,
		SecurityContext:        security,
		RequestID:              testInvocationRecordRequestID(t, "11111111111111111111111111111111"),
		TraceID:                testInvocationRecordTraceID(t, "22222222222222222222222222222222"),
		InvocationID:           testInvocationRecordID(t, "33333333333333333333333333333333"),
		ExecutionClass:         audit.ExecutionLocal,
		StartedAt:              startedAt,
		CompletedAt:            startedAt.Add(time.Millisecond),
		Duration:               time.Millisecond,
		Outcome:                audit.NewSucceededOutcome(),
	}
}

func testSecurityContext(t *testing.T, principal audit.Principal) audit.SecurityContext {
	t.Helper()
	security, err := audit.NewSecurityContext(audit.SecurityContextOptions{CallerPrincipal: principal})
	if err != nil {
		t.Fatalf("NewSecurityContext: %v", err)
	}
	return security
}

func testInvocationRecordRequestID(t *testing.T, value string) audit.RequestID {
	t.Helper()
	id, err := audit.ParseRequestID(value)
	if err != nil {
		t.Fatalf("ParseRequestID: %v", err)
	}
	return id
}

func testInvocationRecordTraceID(t *testing.T, value string) audit.TraceID {
	t.Helper()
	id, err := audit.ParseTraceID(value)
	if err != nil {
		t.Fatalf("ParseTraceID: %v", err)
	}
	return id
}

func testInvocationRecordID(t *testing.T, value string) audit.InvocationID {
	t.Helper()
	id, err := audit.ParseInvocationID(value)
	if err != nil {
		t.Fatalf("ParseInvocationID: %v", err)
	}
	return id
}
