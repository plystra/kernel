package invocation

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/plystra/kernel/audit"
	"github.com/plystra/kernel/capability"
	"github.com/plystra/kernel/plugin"
)

type invokeRequest struct {
	Value string
}

type invokeResponse struct {
	Value string
}

func TestHandleInvokeRunsCompleteRawDispatchPath(t *testing.T) {
	t.Parallel()

	contract := capability.MustParseContract[invokeRequest, invokeResponse]("example.invoke/v1")
	security := testGovernedSecurityContext(t)
	sink := &invokeCaptureSink{}
	var providerContext InvocationContext
	harness := newInvokeHarness(t, contract, func(ctx context.Context, request invokeRequest) (invokeResponse, error) {
		current, exists := Current(ctx)
		if !exists {
			t.Fatal("provider did not receive governed invocation context")
		}
		deadline, hasDeadline := ctx.Deadline()
		if !hasDeadline || !deadline.Equal(current.Deadline()) {
			t.Fatalf("provider deadline = %v / %v", deadline, current.Deadline())
		}
		providerContext = current
		return invokeResponse{Value: "handled:" + request.Value}, nil
	}, invokeHarnessOptions{sink: sink, security: security})

	response, err := harness.handle.Invoke(harness.root, invokeRequest{Value: "request"})
	if err != nil || response.Value != "handled:request" {
		t.Fatalf("Invoke = %#v, %v", response, err)
	}
	records := sink.Records()
	if len(records) != 1 {
		t.Fatalf("audit records = %d, want 1", len(records))
	}
	record := records[0]
	rootRequestID, _ := RequestID(harness.root)
	rootTraceID, _ := TraceID(harness.root)
	if !record.Valid() || record.RuntimeCaller() != harness.handle.scope.caller || record.Capability() != contract.Identifier() ||
		record.CapabilitySchemaDigest() != harness.binding.SchemaDigest() || record.ProviderKind() != audit.ProviderKindPlugin ||
		record.ProviderPluginID() != harness.binding.ProviderID() || record.ProviderBuild() != harness.binding.ProviderBuild() ||
		record.SecurityContext() != security || record.RequestID() != rootRequestID || record.TraceID() != rootTraceID ||
		record.InvocationID() != providerContext.InvocationID() || record.ParentInvocationID().Valid() ||
		record.ExecutionClass() != audit.ExecutionLocal || record.StartedAt().After(record.CompletedAt()) || record.Duration() < 0 ||
		record.Outcome().Status() != audit.OutcomeSucceeded {
		t.Fatalf("audit record = %#v", record)
	}
}

func TestHandleInvokeAuditsKernelProviderWithoutFabricatedPluginID(t *testing.T) {
	t.Parallel()

	contract := capability.MustParseContract[invokeRequest, invokeResponse]("kernel.info/v1")
	endpoint, err := NewEndpoint(contract, func(context.Context, invokeRequest) (invokeResponse, error) {
		return invokeResponse{Value: "kernel"}, nil
	})
	if err != nil {
		t.Fatalf("NewEndpoint: %v", err)
	}
	binding, err := NewBinding(BindingOptions{
		ProviderKind:  ProviderKindKernel,
		ProviderBuild: mustModuleBuild(t, "github.com/plystra/kernel", "v0.1.0", ""),
		SchemaDigest:  sha256.Sum256([]byte("kernel.info/v1 schema")),
	}, endpoint)
	if err != nil {
		t.Fatalf("NewBinding: %v", err)
	}
	sink := &invokeCaptureSink{}
	dispatcher := newInvokeDispatcher(t, sink, time.Second)
	catalog, err := NewCatalog([]Binding{binding})
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	if err := dispatcher.Publish(catalog); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	handle, root := newInvokeHandleAndRoot(t, dispatcher, contract, testAnonymousSecurityContext(t))
	response, err := handle.Invoke(root, invokeRequest{})
	if err != nil || response.Value != "kernel" {
		t.Fatalf("Invoke = %#v, %v", response, err)
	}
	records := sink.Records()
	if len(records) != 1 || records[0].ProviderKind() != audit.ProviderKindKernel || records[0].ProviderPluginID().String() != "" {
		t.Fatalf("Kernel provider records = %#v", records)
	}
}

func TestHandleInvokePropagatesNestedAncestryAndSecurity(t *testing.T) {
	t.Parallel()

	outerContract := capability.MustParseContract[invokeRequest, invokeResponse]("example.outer/v1")
	innerContract := capability.MustParseContract[invokeRequest, invokeResponse]("example.inner/v1")
	outerProviderID := mustPluginID(t, "acme.invoke.outer")
	innerProviderID := mustPluginID(t, "acme.invoke.inner")
	callerID := mustPluginID(t, "acme.invoke.caller")
	security := testGovernedSecurityContext(t)
	sink := &invokeCaptureSink{}
	recorder := mustInvocationRecorder(t, sink)
	dispatcher, err := NewDispatcher(DispatcherOptions{
		DefaultTimeout: time.Second,
		AuditRecorder:  recorder,
	})
	if err != nil {
		t.Fatalf("NewDispatcher: %v", err)
	}

	outerProviderCaller, err := audit.NewPluginCallerIdentity(outerProviderID)
	if err != nil {
		t.Fatalf("NewPluginCallerIdentity(outer provider): %v", err)
	}
	innerScope, err := dispatcher.Scope(outerProviderCaller)
	if err != nil {
		t.Fatalf("inner Scope: %v", err)
	}
	innerHandle, err := NewHandle(innerScope, innerContract, true)
	if err != nil {
		t.Fatalf("NewHandle(inner): %v", err)
	}
	innerBinding := newInvokeBinding(t, innerContract, innerProviderID, func(_ context.Context, request invokeRequest) (invokeResponse, error) {
		return invokeResponse{Value: "inner:" + request.Value}, nil
	})
	outerBinding := newInvokeBinding(t, outerContract, outerProviderID, func(ctx context.Context, request invokeRequest) (invokeResponse, error) {
		inner, err := innerHandle.Invoke(ctx, request)
		if err != nil {
			return invokeResponse{}, err
		}
		return invokeResponse{Value: "outer:" + inner.Value}, nil
	})
	catalog, err := NewCatalog([]Binding{outerBinding, innerBinding})
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	if err := dispatcher.Publish(catalog); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	callerIdentity, err := audit.NewPluginCallerIdentity(callerID)
	if err != nil {
		t.Fatalf("NewPluginCallerIdentity(caller): %v", err)
	}
	outerScope, err := dispatcher.Scope(callerIdentity)
	if err != nil {
		t.Fatalf("outer Scope: %v", err)
	}
	outerHandle, err := NewHandle(outerScope, outerContract, true)
	if err != nil {
		t.Fatalf("NewHandle(outer): %v", err)
	}
	root := newInvokeRoot(t, dispatcher, context.Background(), security)
	response, err := outerHandle.Invoke(root, invokeRequest{Value: "request"})
	if err != nil || response.Value != "outer:inner:request" {
		t.Fatalf("outer Invoke = %#v, %v", response, err)
	}

	records := sink.Records()
	if len(records) != 2 {
		t.Fatalf("records = %d, want 2", len(records))
	}
	byCapability := make(map[string]audit.InvocationRecord, len(records))
	for _, record := range records {
		byCapability[record.Capability().String()] = record
	}
	outer := byCapability[outerContract.Identifier().String()]
	inner := byCapability[innerContract.Identifier().String()]
	if !outer.Valid() || !inner.Valid() || outer.RuntimeCaller() != callerIdentity || inner.RuntimeCaller() != outerProviderCaller ||
		outer.ProviderPluginID() != outerProviderID || inner.ProviderPluginID() != innerProviderID ||
		inner.ParentInvocationID() != outer.InvocationID() || outer.ParentInvocationID().Valid() ||
		inner.RequestID() != outer.RequestID() || inner.TraceID() != outer.TraceID() ||
		inner.SecurityContext() != security || outer.SecurityContext() != security ||
		inner.Outcome().Status() != audit.OutcomeSucceeded || outer.Outcome().Status() != audit.OutcomeSucceeded {
		t.Fatalf("nested records = outer %#v / inner %#v", outer, inner)
	}
}

func TestHandleInvokeRejectsNestedCallThatDropsGovernedContext(t *testing.T) {
	t.Parallel()

	outerContract := capability.MustParseContract[invokeRequest, invokeResponse]("example.context-outer/v1")
	innerContract := capability.MustParseContract[invokeRequest, invokeResponse]("example.context-inner/v1")
	outerProviderID := mustPluginID(t, "acme.context.outer")
	innerProviderID := mustPluginID(t, "acme.context.inner")
	sink := &invokeCaptureSink{}
	dispatcher := newInvokeDispatcher(t, sink, time.Second)
	outerProviderCaller, err := audit.NewPluginCallerIdentity(outerProviderID)
	if err != nil {
		t.Fatalf("NewPluginCallerIdentity: %v", err)
	}
	innerScope, err := dispatcher.Scope(outerProviderCaller)
	if err != nil {
		t.Fatalf("inner Scope: %v", err)
	}
	innerHandle, err := NewHandle(innerScope, innerContract, true)
	if err != nil {
		t.Fatalf("NewHandle(inner): %v", err)
	}
	var innerProviderCalls atomic.Int32
	innerBinding := newInvokeBinding(t, innerContract, innerProviderID, func(context.Context, invokeRequest) (invokeResponse, error) {
		innerProviderCalls.Add(1)
		return invokeResponse{}, nil
	})
	outerBinding := newInvokeBinding(t, outerContract, outerProviderID, func(context.Context, invokeRequest) (invokeResponse, error) {
		return innerHandle.Invoke(context.Background(), invokeRequest{})
	})
	catalog, err := NewCatalog([]Binding{outerBinding, innerBinding})
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	if err := dispatcher.Publish(catalog); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	outerHandle, root := newInvokeHandleAndRoot(t, dispatcher, outerContract, testGovernedSecurityContext(t))
	response, err := outerHandle.Invoke(root, invokeRequest{})
	requireInvocationError(t, err, audit.ErrorInvalidArgument, detailGovernedContextRequired)
	if response != (invokeResponse{}) || innerProviderCalls.Load() != 0 {
		t.Fatalf("dropped nested context = %#v, %v, inner provider calls %d", response, err, innerProviderCalls.Load())
	}
	records := sink.Records()
	if len(records) != 1 || records[0].Capability() != outerContract.Identifier() ||
		records[0].Outcome().ErrorCode() != audit.ErrorInvalidArgument || records[0].Outcome().DetailCode() != detailGovernedContextRequired {
		t.Fatalf("dropped nested context records = %#v", records)
	}
}

func TestHandleInvokeIsSafeForConcurrentRootCalls(t *testing.T) {
	t.Parallel()

	contract := capability.MustParseContract[invokeRequest, invokeResponse]("example.concurrent-invoke/v1")
	sink := &invokeCaptureSink{}
	harness := newInvokeHarness(t, contract, func(_ context.Context, request invokeRequest) (invokeResponse, error) {
		return invokeResponse(request), nil
	}, invokeHarnessOptions{sink: sink})

	const calls = 128
	var group sync.WaitGroup
	group.Add(calls)
	for index := range calls {
		go func(value string) {
			defer group.Done()
			response, err := harness.handle.Invoke(harness.root, invokeRequest{Value: value})
			if err != nil || response.Value != value {
				t.Errorf("Invoke(%q) = %#v, %v", value, response, err)
			}
		}(fmt.Sprintf("request-%d", index))
	}
	group.Wait()

	records := sink.Records()
	requestID, _ := RequestID(harness.root)
	traceID, _ := TraceID(harness.root)
	invocations := make(map[audit.InvocationID]struct{}, len(records))
	for _, record := range records {
		if !record.Valid() || record.RequestID() != requestID || record.TraceID() != traceID ||
			record.ParentInvocationID().Valid() || record.Outcome().Status() != audit.OutcomeSucceeded {
			t.Fatalf("concurrent record = %#v", record)
		}
		invocations[record.InvocationID()] = struct{}{}
	}
	if len(records) != calls || len(invocations) != calls {
		t.Fatalf("records/unique invocations = %d/%d, want %d", len(records), len(invocations), calls)
	}
}

func TestHandleInvokeNormalizesProviderFailuresAndPanics(t *testing.T) {
	t.Parallel()

	safe, err := NewError(audit.ErrorInvalidArgument, "contract.invalid_request")
	if err != nil {
		t.Fatalf("NewError: %v", err)
	}
	unknown, err := NewError(audit.ErrorResultUnknown, "transport.delivery_unknown")
	if err != nil {
		t.Fatalf("NewError(result unknown): %v", err)
	}
	for _, test := range []struct {
		name       string
		handler    capability.Handler[invokeRequest, invokeResponse]
		code       audit.ErrorCode
		detail     string
		status     audit.OutcomeStatus
		wantShared *Error
	}{
		{name: "safe error", handler: func(context.Context, invokeRequest) (invokeResponse, error) {
			return invokeResponse{Value: "must not escape"}, safe
		}, code: audit.ErrorInvalidArgument, detail: "contract.invalid_request", status: audit.OutcomeFailed, wantShared: safe},
		{name: "wrapped safe error", handler: func(context.Context, invokeRequest) (invokeResponse, error) {
			return invokeResponse{}, fmt.Errorf("provider password=secret: %w", safe)
		}, code: audit.ErrorInvalidArgument, detail: "contract.invalid_request", status: audit.OutcomeFailed, wantShared: safe},
		{name: "result unknown", handler: func(context.Context, invokeRequest) (invokeResponse, error) {
			return invokeResponse{}, unknown
		}, code: audit.ErrorResultUnknown, detail: "transport.delivery_unknown", status: audit.OutcomeResultUnknown, wantShared: unknown},
		{name: "raw sensitive error", handler: func(context.Context, invokeRequest) (invokeResponse, error) {
			return invokeResponse{}, errors.New("provider password=secret")
		}, code: audit.ErrorInternal, detail: detailProviderFailed, status: audit.OutcomeFailed},
		{name: "normalizer panic", handler: func(context.Context, invokeRequest) (invokeResponse, error) {
			return invokeResponse{}, panickingProviderError{}
		}, code: audit.ErrorInternal, detail: detailProviderFailed, status: audit.OutcomeFailed},
		{name: "panic", handler: func(context.Context, invokeRequest) (invokeResponse, error) {
			panic("provider token=secret")
		}, code: audit.ErrorInternal, detail: detailProviderPanic, status: audit.OutcomeFailed},
		{name: "deadline error", handler: func(context.Context, invokeRequest) (invokeResponse, error) {
			return invokeResponse{}, context.DeadlineExceeded
		}, code: audit.ErrorTimeout, detail: detailDeadlineExceeded, status: audit.OutcomeTimedOut},
		{name: "cancelled error", handler: func(context.Context, invokeRequest) (invokeResponse, error) {
			return invokeResponse{}, context.Canceled
		}, code: audit.ErrorCancelled, detail: detailInvocationCancelled, status: audit.OutcomeCancelled},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			contract := capability.MustParseContract[invokeRequest, invokeResponse]("example.provider-failure/v1")
			sink := &invokeCaptureSink{}
			harness := newInvokeHarness(t, contract, test.handler, invokeHarnessOptions{sink: sink})
			response, err := harness.handle.Invoke(harness.root, invokeRequest{})
			boundary := requireInvocationError(t, err, test.code, test.detail)
			if response != (invokeResponse{}) || strings.Contains(err.Error(), "secret") {
				t.Fatalf("provider failure = %#v, %v", response, err)
			}
			if test.wantShared != nil && boundary != test.wantShared {
				t.Fatalf("safe boundary copy = %#v, want original %#v", boundary, test.wantShared)
			}
			records := sink.Records()
			if len(records) != 1 || records[0].Outcome().Status() != test.status ||
				records[0].Outcome().ErrorCode() != test.code || records[0].Outcome().DetailCode() != test.detail {
				t.Fatalf("provider failure records = %#v", records)
			}
		})
	}
}

func TestHandleInvokeAppliesDeadlineToProvider(t *testing.T) {
	t.Parallel()

	contract := capability.MustParseContract[invokeRequest, invokeResponse]("example.deadline/v1")
	sink := &invokeCaptureSink{}
	var providerCalls atomic.Int32
	harness := newInvokeHarness(t, contract, func(ctx context.Context, _ invokeRequest) (invokeResponse, error) {
		providerCalls.Add(1)
		<-ctx.Done()
		return invokeResponse{Value: "late success"}, nil
	}, invokeHarnessOptions{sink: sink, defaultTimeout: 15 * time.Millisecond})

	response, err := harness.handle.Invoke(harness.root, invokeRequest{})
	requireInvocationError(t, err, audit.ErrorTimeout, detailDeadlineExceeded)
	if !errors.Is(err, context.DeadlineExceeded) || response != (invokeResponse{}) || providerCalls.Load() != 1 {
		t.Fatalf("deadline Invoke = %#v, %v, provider calls %d", response, err, providerCalls.Load())
	}
	records := sink.Records()
	if len(records) != 1 || records[0].Outcome().Status() != audit.OutcomeTimedOut || records[0].Duration() < 10*time.Millisecond {
		t.Fatalf("deadline records = %#v", records)
	}
}

func TestHandleInvokePropagatesTrustedCancellation(t *testing.T) {
	t.Parallel()

	parent, cancel := context.WithCancel(context.Background())
	contract := capability.MustParseContract[invokeRequest, invokeResponse]("example.cancel/v1")
	sink := &invokeCaptureSink{}
	harness := newInvokeHarness(t, contract, func(ctx context.Context, _ invokeRequest) (invokeResponse, error) {
		cancel()
		<-ctx.Done()
		return invokeResponse{}, ctx.Err()
	}, invokeHarnessOptions{sink: sink, parent: parent})

	response, err := harness.handle.Invoke(harness.root, invokeRequest{})
	requireInvocationError(t, err, audit.ErrorCancelled, detailInvocationCancelled)
	if !errors.Is(err, context.Canceled) || response != (invokeResponse{}) {
		t.Fatalf("cancelled Invoke = %#v, %v", response, err)
	}
	records := sink.Records()
	if len(records) != 1 || records[0].Outcome().Status() != audit.OutcomeCancelled {
		t.Fatalf("cancellation records = %#v", records)
	}
}

func TestHandleInvokeAuditsPreCancelledTrustedAuthorityThroughDetachedContext(t *testing.T) {
	t.Parallel()

	parent, cancel := context.WithCancel(context.Background())
	contract := capability.MustParseContract[invokeRequest, invokeResponse]("example.pre-cancel/v1")
	sink := &invokeCaptureSink{}
	var providerCalls atomic.Int32
	harness := newInvokeHarness(t, contract, func(context.Context, invokeRequest) (invokeResponse, error) {
		providerCalls.Add(1)
		return invokeResponse{}, nil
	}, invokeHarnessOptions{sink: sink, parent: parent})
	cancel()
	detached := context.WithoutCancel(harness.root)
	if detached.Err() != nil {
		t.Fatalf("detached context error = %v", detached.Err())
	}

	response, err := harness.handle.Invoke(detached, invokeRequest{})
	requireInvocationError(t, err, audit.ErrorCancelled, detailInvocationCancelled)
	if !errors.Is(err, context.Canceled) || response != (invokeResponse{}) || providerCalls.Load() != 0 {
		t.Fatalf("pre-cancelled Invoke = %#v, %v, provider calls %d", response, err, providerCalls.Load())
	}
	records := sink.Records()
	if len(records) != 1 || records[0].Outcome().Status() != audit.OutcomeCancelled {
		t.Fatalf("pre-cancelled records = %#v", records)
	}
}

func TestHandleInvokeRechecksTrustedCancellationAfterFastProviderReturn(t *testing.T) {
	t.Parallel()

	parent, cancel := context.WithCancel(context.Background())
	contract := capability.MustParseContract[invokeRequest, invokeResponse]("example.fast-cancel/v1")
	sink := &invokeCaptureSink{}
	harness := newInvokeHarness(t, contract, func(context.Context, invokeRequest) (invokeResponse, error) {
		cancel()
		return invokeResponse{Value: "must not escape"}, nil
	}, invokeHarnessOptions{sink: sink, parent: parent})

	response, err := harness.handle.Invoke(harness.root, invokeRequest{})
	requireInvocationError(t, err, audit.ErrorCancelled, detailInvocationCancelled)
	if !errors.Is(err, context.Canceled) || response != (invokeResponse{}) {
		t.Fatalf("fast cancellation Invoke = %#v, %v", response, err)
	}
	records := sink.Records()
	if len(records) != 1 || records[0].Outcome().Status() != audit.OutcomeCancelled {
		t.Fatalf("fast cancellation records = %#v", records)
	}
}

func TestHandleInvokeClassifiesTrustedAuthorityDeadline(t *testing.T) {
	t.Parallel()

	parent, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	defer cancel()
	contract := capability.MustParseContract[invokeRequest, invokeResponse]("example.authority-deadline/v1")
	sink := &invokeCaptureSink{}
	harness := newInvokeHarness(t, contract, func(ctx context.Context, _ invokeRequest) (invokeResponse, error) {
		<-ctx.Done()
		return invokeResponse{}, ctx.Err()
	}, invokeHarnessOptions{sink: sink, parent: parent, defaultTimeout: time.Second})

	response, err := harness.handle.Invoke(harness.root, invokeRequest{})
	requireInvocationError(t, err, audit.ErrorTimeout, detailDeadlineExceeded)
	if !errors.Is(err, context.DeadlineExceeded) || response != (invokeResponse{}) {
		t.Fatalf("authority deadline Invoke = %#v, %v", response, err)
	}
	records := sink.Records()
	if len(records) != 1 || records[0].Outcome().Status() != audit.OutcomeTimedOut {
		t.Fatalf("authority deadline records = %#v", records)
	}
}

func TestHandleInvokeAuditsContractMismatchWithoutCallingProvider(t *testing.T) {
	t.Parallel()

	providerContract := capability.MustParseContract[invokeRequest, invokeResponse]("example.contract/v1")
	callerContract := capability.MustParseContract[invokeRequest, invokeResponse]("example.contract/v1")
	sink := &invokeCaptureSink{}
	var providerCalls atomic.Int32
	harness := newInvokeHarness(t, providerContract, func(context.Context, invokeRequest) (invokeResponse, error) {
		providerCalls.Add(1)
		return invokeResponse{}, nil
	}, invokeHarnessOptions{sink: sink})
	handle, err := NewHandle(harness.handle.scope, callerContract, true)
	if err != nil {
		t.Fatalf("NewHandle: %v", err)
	}
	response, err := handle.Invoke(harness.root, invokeRequest{})
	requireInvocationError(t, err, audit.ErrorInternal, detailContractMismatch)
	if response != (invokeResponse{}) || providerCalls.Load() != 0 {
		t.Fatalf("contract mismatch = %#v, %v, provider calls %d", response, err, providerCalls.Load())
	}
	records := sink.Records()
	if len(records) != 1 || records[0].Outcome().DetailCode() != detailContractMismatch {
		t.Fatalf("contract mismatch records = %#v", records)
	}
}

func TestHandleInvokeFailsClosedWhenAuditDeliveryFails(t *testing.T) {
	t.Parallel()

	contract := capability.MustParseContract[invokeRequest, invokeResponse]("example.audit-failure/v1")
	sink := &rejectingInvokeSink{}
	var providerCalls atomic.Int32
	harness := newInvokeHarness(t, contract, func(context.Context, invokeRequest) (invokeResponse, error) {
		providerCalls.Add(1)
		return invokeResponse{Value: "must not escape"}, nil
	}, invokeHarnessOptions{sink: sink})

	response, err := harness.handle.Invoke(harness.root, invokeRequest{})
	requireInvocationError(t, err, audit.ErrorResultUnknown, detailAuditRecordingFailed)
	if response != (invokeResponse{}) || providerCalls.Load() != 1 || strings.Contains(err.Error(), "secret") {
		t.Fatalf("audit failure = %#v, %v, provider calls %d", response, err, providerCalls.Load())
	}
	attempts := sink.Attempts()
	if len(attempts) != 1 || !attempts[0].Valid() || attempts[0].Outcome().Status() != audit.OutcomeSucceeded {
		t.Fatalf("audit attempts = %#v", attempts)
	}
}

func TestHandleInvokeRejectsPreEntryFailuresWithoutProviderOrAudit(t *testing.T) {
	t.Parallel()

	contract := capability.MustParseContract[invokeRequest, invokeResponse]("example.pre-entry/v1")
	sink := &invokeCaptureSink{}
	var providerCalls atomic.Int32
	harness := newInvokeHarness(t, contract, func(context.Context, invokeRequest) (invokeResponse, error) {
		providerCalls.Add(1)
		return invokeResponse{}, nil
	}, invokeHarnessOptions{sink: sink})

	var zero Handle[invokeRequest, invokeResponse]
	_, err := zero.Invoke(context.Background(), invokeRequest{})
	requireInvocationError(t, err, audit.ErrorInternal, detailInvalidHandle)
	var nilContext context.Context
	_, err = harness.handle.Invoke(nilContext, invokeRequest{})
	requireInvocationError(t, err, audit.ErrorInvalidArgument, detailGovernedContextRequired)
	_, err = harness.handle.Invoke(context.Background(), invokeRequest{})
	requireInvocationError(t, err, audit.ErrorInvalidArgument, detailGovernedContextRequired)
	unavailable, err := NewHandle(harness.handle.scope, contract, false)
	if err != nil {
		t.Fatalf("NewHandle(unavailable): %v", err)
	}
	_, err = unavailable.Invoke(harness.root, invokeRequest{})
	requireInvocationError(t, err, audit.ErrorUnavailable, detailCapabilityUnavailable)

	unpublishedSink := &invokeCaptureSink{}
	unpublished := newInvokeDispatcher(t, unpublishedSink, time.Second)
	unpublishedHandle, unpublishedRoot := newInvokeHandleAndRoot(t, unpublished, contract, testAnonymousSecurityContext(t))
	_, err = unpublishedHandle.Invoke(unpublishedRoot, invokeRequest{})
	requireInvocationError(t, err, audit.ErrorUnavailable, detailDispatcherNotReady)

	emptySink := &invokeCaptureSink{}
	empty := newInvokeDispatcher(t, emptySink, time.Second)
	emptyCatalog, err := NewCatalog(nil)
	if err != nil {
		t.Fatalf("NewCatalog(nil): %v", err)
	}
	if err := empty.Publish(emptyCatalog); err != nil {
		t.Fatalf("Publish(empty): %v", err)
	}
	emptyHandle, emptyRoot := newInvokeHandleAndRoot(t, empty, contract, testAnonymousSecurityContext(t))
	_, err = emptyHandle.Invoke(emptyRoot, invokeRequest{})
	requireInvocationError(t, err, audit.ErrorUnavailable, detailCapabilityUnavailable)

	if providerCalls.Load() != 0 || len(sink.Records()) != 0 || len(unpublishedSink.Records()) != 0 || len(emptySink.Records()) != 0 {
		t.Fatalf("pre-entry work reached provider or audit: provider=%d records=%d/%d/%d", providerCalls.Load(), len(sink.Records()), len(unpublishedSink.Records()), len(emptySink.Records()))
	}
}

func BenchmarkCapabilityInvoke(b *testing.B) {
	handle, root, _ := benchmarkInvokeRuntime(b, benchmarkNoopInvocationSink{})
	request := invokeRequest{Value: "request"}
	var response invokeResponse
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		var err error
		response, err = handle.Invoke(root, request)
		if err != nil {
			b.Fatal(err)
		}
	}
	benchmarkInvokeResponse = response
}

func BenchmarkCapabilityInvokeWithAudit(b *testing.B) {
	sink := &benchmarkCountingInvocationSink{}
	handle, root, _ := benchmarkInvokeRuntime(b, sink)
	request := invokeRequest{Value: "request"}
	var response invokeResponse
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		var err error
		response, err = handle.Invoke(root, request)
		if err != nil {
			b.Fatal(err)
		}
	}
	benchmarkInvokeResponse = response
	benchmarkAuditCount = sink.count.Load()
}

type invokeHarnessOptions struct {
	sink           audit.InvocationSink
	security       audit.SecurityContext
	parent         context.Context
	defaultTimeout time.Duration
}

type invokeHarness struct {
	dispatcher *Dispatcher
	handle     Handle[invokeRequest, invokeResponse]
	root       context.Context
	binding    Binding
}

func newInvokeHarness(
	t *testing.T,
	contract capability.Contract[invokeRequest, invokeResponse],
	handler capability.Handler[invokeRequest, invokeResponse],
	options invokeHarnessOptions,
) invokeHarness {
	t.Helper()
	if options.sink == nil {
		t.Fatal("newInvokeHarness requires an audit sink")
	}
	if !options.security.Valid() {
		options.security = testAnonymousSecurityContext(t)
	}
	if options.parent == nil {
		options.parent = context.Background()
	}
	if options.defaultTimeout == 0 {
		options.defaultTimeout = time.Second
	}
	providerID := mustPluginID(t, "acme.invoke.provider")
	binding := newInvokeBinding(t, contract, providerID, handler)
	dispatcher := newInvokeDispatcher(t, options.sink, options.defaultTimeout)
	catalog, err := NewCatalog([]Binding{binding})
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	if err := dispatcher.Publish(catalog); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	callerID := mustPluginID(t, "acme.invoke.caller")
	caller, err := audit.NewPluginCallerIdentity(callerID)
	if err != nil {
		t.Fatalf("NewPluginCallerIdentity: %v", err)
	}
	scope, err := dispatcher.Scope(caller)
	if err != nil {
		t.Fatalf("Scope: %v", err)
	}
	handle, err := NewHandle(scope, contract, true)
	if err != nil {
		t.Fatalf("NewHandle: %v", err)
	}
	return invokeHarness{
		dispatcher: dispatcher,
		handle:     handle,
		root:       newInvokeRoot(t, dispatcher, options.parent, options.security),
		binding:    binding,
	}
}

func newInvokeDispatcher(t *testing.T, sink audit.InvocationSink, timeout time.Duration) *Dispatcher {
	t.Helper()
	dispatcher, err := NewDispatcher(DispatcherOptions{
		DefaultTimeout: timeout,
		AuditRecorder:  mustInvocationRecorder(t, sink),
	})
	if err != nil {
		t.Fatalf("NewDispatcher: %v", err)
	}
	return dispatcher
}

func newInvokeHandleAndRoot(
	t *testing.T,
	dispatcher *Dispatcher,
	contract capability.Contract[invokeRequest, invokeResponse],
	security audit.SecurityContext,
) (Handle[invokeRequest, invokeResponse], context.Context) {
	t.Helper()
	caller, err := audit.NewPluginCallerIdentity(mustPluginID(t, "acme.invoke.caller"))
	if err != nil {
		t.Fatalf("NewPluginCallerIdentity: %v", err)
	}
	scope, err := dispatcher.Scope(caller)
	if err != nil {
		t.Fatalf("Scope: %v", err)
	}
	handle, err := NewHandle(scope, contract, true)
	if err != nil {
		t.Fatalf("NewHandle: %v", err)
	}
	return handle, newInvokeRoot(t, dispatcher, context.Background(), security)
}

func newInvokeRoot(t *testing.T, dispatcher *Dispatcher, parent context.Context, security audit.SecurityContext) context.Context {
	t.Helper()
	kernelScope, err := dispatcher.Scope(audit.NewKernelCallerIdentity())
	if err != nil {
		t.Fatalf("Kernel Scope: %v", err)
	}
	root, err := kernelScope.NewRootContext(parent, security)
	if err != nil {
		t.Fatalf("NewRootContext: %v", err)
	}
	return root
}

func newInvokeBinding(
	t *testing.T,
	contract capability.Contract[invokeRequest, invokeResponse],
	providerID plugin.ID,
	handler capability.Handler[invokeRequest, invokeResponse],
) Binding {
	t.Helper()
	endpoint, err := NewEndpoint(contract, handler)
	if err != nil {
		t.Fatalf("NewEndpoint: %v", err)
	}
	binding, err := NewBinding(BindingOptions{
		ProviderKind:  ProviderKindPlugin,
		ProviderID:    providerID,
		ProviderBuild: mustModuleBuild(t, "github.com/acme/invoke", "v1.0.0", ""),
		SchemaDigest:  sha256.Sum256([]byte(contract.Identifier().String() + " schema")),
	}, endpoint)
	if err != nil {
		t.Fatalf("NewBinding: %v", err)
	}
	return binding
}

func mustInvocationRecorder(t *testing.T, sink audit.InvocationSink) *audit.InvocationRecorder {
	t.Helper()
	recorder, err := audit.NewInvocationRecorder(sink, time.Second)
	if err != nil {
		t.Fatalf("NewInvocationRecorder: %v", err)
	}
	return recorder
}

func requireInvocationError(t *testing.T, err error, code audit.ErrorCode, detail string) *Error {
	t.Helper()
	var boundary *Error
	if !errors.As(err, &boundary) || !boundary.valid() || boundary.Code() != code || boundary.DetailCode() != detail {
		t.Fatalf("invocation error = %#v / %v, want %s:%s", boundary, err, code, detail)
	}
	return boundary
}

type invokeCaptureSink struct {
	mu      sync.Mutex
	records []audit.InvocationRecord
}

func (s *invokeCaptureSink) PersistInvocation(_ context.Context, record audit.InvocationRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, record)
	return nil
}

func (*invokeCaptureSink) Flush(context.Context) error { return nil }

func (s *invokeCaptureSink) Records() []audit.InvocationRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]audit.InvocationRecord(nil), s.records...)
}

type rejectingInvokeSink struct {
	mu       sync.Mutex
	attempts []audit.InvocationRecord
}

type panickingProviderError struct{}

func (panickingProviderError) Error() string { return "provider password=secret" }

func (panickingProviderError) As(any) bool { panic("provider token=secret") }

func (s *rejectingInvokeSink) PersistInvocation(_ context.Context, record audit.InvocationRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attempts = append(s.attempts, record)
	return errors.New("audit database password=secret")
}

func (*rejectingInvokeSink) Flush(context.Context) error { return nil }

func (s *rejectingInvokeSink) Attempts() []audit.InvocationRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]audit.InvocationRecord(nil), s.attempts...)
}

type benchmarkNoopInvocationSink struct{}

func (benchmarkNoopInvocationSink) PersistInvocation(context.Context, audit.InvocationRecord) error {
	return nil
}

func (benchmarkNoopInvocationSink) Flush(context.Context) error { return nil }

type benchmarkCountingInvocationSink struct {
	count atomic.Uint64
}

func (s *benchmarkCountingInvocationSink) PersistInvocation(_ context.Context, record audit.InvocationRecord) error {
	if !record.Valid() {
		return audit.ErrInvalidInvocationRecord
	}
	s.count.Add(1)
	return nil
}

func (*benchmarkCountingInvocationSink) Flush(context.Context) error { return nil }

func benchmarkInvokeRuntime(b *testing.B, sink audit.InvocationSink) (Handle[invokeRequest, invokeResponse], context.Context, Binding) {
	b.Helper()
	contract := capability.MustParseContract[invokeRequest, invokeResponse]("example.benchmark-invoke/v1")
	endpoint, err := NewEndpoint(contract, func(_ context.Context, request invokeRequest) (invokeResponse, error) {
		return invokeResponse(request), nil
	})
	if err != nil {
		b.Fatalf("NewEndpoint: %v", err)
	}
	providerID, err := plugin.ParseID("acme.benchmark.provider")
	if err != nil {
		b.Fatalf("ParseID(provider): %v", err)
	}
	binding, err := NewBinding(BindingOptions{
		ProviderKind:  ProviderKindPlugin,
		ProviderID:    providerID,
		ProviderBuild: mustModuleBuildBenchmark(b, "github.com/acme/benchmark", "v1.0.0", ""),
		SchemaDigest:  sha256.Sum256([]byte("example.benchmark-invoke/v1 schema")),
	}, endpoint)
	if err != nil {
		b.Fatalf("NewBinding: %v", err)
	}
	catalog, err := NewCatalog([]Binding{binding})
	if err != nil {
		b.Fatalf("NewCatalog: %v", err)
	}
	recorder, err := audit.NewInvocationRecorder(sink, time.Second)
	if err != nil {
		b.Fatalf("NewInvocationRecorder: %v", err)
	}
	dispatcher, err := NewDispatcher(DispatcherOptions{DefaultTimeout: time.Second, AuditRecorder: recorder})
	if err != nil {
		b.Fatalf("NewDispatcher: %v", err)
	}
	if err := dispatcher.Publish(catalog); err != nil {
		b.Fatalf("Publish: %v", err)
	}
	callerID, err := plugin.ParseID("acme.benchmark.caller")
	if err != nil {
		b.Fatalf("ParseID(caller): %v", err)
	}
	caller, err := audit.NewPluginCallerIdentity(callerID)
	if err != nil {
		b.Fatalf("NewPluginCallerIdentity: %v", err)
	}
	callerScope, err := dispatcher.Scope(caller)
	if err != nil {
		b.Fatalf("Scope(caller): %v", err)
	}
	handle, err := NewHandle(callerScope, contract, true)
	if err != nil {
		b.Fatalf("NewHandle: %v", err)
	}
	kernelScope, err := dispatcher.Scope(audit.NewKernelCallerIdentity())
	if err != nil {
		b.Fatalf("Scope(Kernel): %v", err)
	}
	security, err := audit.NewSecurityContext(audit.SecurityContextOptions{CallerPrincipal: audit.NewAnonymousPrincipal()})
	if err != nil {
		b.Fatalf("NewSecurityContext: %v", err)
	}
	root, err := kernelScope.NewRootContext(context.Background(), security)
	if err != nil {
		b.Fatalf("NewRootContext: %v", err)
	}
	return handle, root, binding
}

var (
	benchmarkInvokeResponse invokeResponse
	benchmarkAuditCount     uint64
)

var _ audit.InvocationSink = (*invokeCaptureSink)(nil)
var _ audit.InvocationSink = (*rejectingInvokeSink)(nil)
var _ audit.InvocationSink = benchmarkNoopInvocationSink{}
var _ audit.InvocationSink = (*benchmarkCountingInvocationSink)(nil)
