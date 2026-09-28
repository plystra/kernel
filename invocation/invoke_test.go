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

	"github.com/plystra/kernel/capability"
	"github.com/plystra/kernel/contextmetadata"
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
	var providerContext InvocationContext
	harness := newInvokeHarness(t, contract, func(ctx context.Context, request invokeRequest) (invokeResponse, error) {
		current, exists := Current(ctx)
		if !exists {
			t.Fatal("provider did not receive invocation context")
		}
		deadline, hasDeadline := ctx.Deadline()
		if !hasDeadline || !deadline.Equal(current.Deadline()) {
			t.Fatalf("provider deadline = %v / %v", deadline, current.Deadline())
		}
		providerContext = current
		return invokeResponse{Value: "handled:" + request.Value}, nil
	}, invokeHarnessOptions{})

	response, err := harness.handle.Invoke(harness.root, invokeRequest{Value: "request"})
	if err != nil || response.Value != "handled:request" {
		t.Fatalf("Invoke = %#v, %v", response, err)
	}
	if !providerContext.RequestID().Valid() || !providerContext.TraceID().Valid() ||
		providerContext.RequestID().String() == providerContext.TraceID().String() ||
		!providerContext.InvocationID().Valid() || providerContext.ParentInvocationID().Valid() {
		t.Fatalf("provider context = %#v", providerContext)
	}
	if _, exists := RequestIDFromContext(harness.root); exists {
		t.Fatal("Invoke mutated its caller context")
	}
}

func TestHandleInvokePreservesOpaqueMetadataWithDefensiveReads(t *testing.T) {
	t.Parallel()
	contract := capability.MustParseContract[invokeRequest, invokeResponse]("example.metadata/v1")
	harness := newInvokeHarness(t, contract, func(ctx context.Context, request invokeRequest) (invokeResponse, error) {
		value, exists, err := contextmetadata.Bytes(ctx, "example.opaque/v1")
		if err != nil || !exists || string(value) != "private metadata" {
			t.Fatalf("governed metadata = %q %t %v", value, exists, err)
		}
		value[0] = 'X'
		return invokeResponse(request), nil
	}, invokeHarnessOptions{})
	ctx, err := contextmetadata.WithBytes(harness.root, "example.opaque/v1", []byte("private metadata"))
	if err != nil {
		t.Fatal(err)
	}
	response, err := harness.handle.Invoke(ctx, invokeRequest{Value: "ordinary request"})
	if err != nil || response.Value != "ordinary request" {
		t.Fatalf("Invoke = %#v %v", response, err)
	}
	value, exists, err := contextmetadata.Bytes(ctx, "example.opaque/v1")
	if err != nil || !exists || string(value) != "private metadata" {
		t.Fatalf("caller metadata = %q %t %v", value, exists, err)
	}
}

func TestHandleInvokeDispatchesIntrinsicImplementation(t *testing.T) {
	t.Parallel()

	contract := capability.MustParseContract[invokeRequest, invokeResponse]("kernel.info/v1")
	endpoint, err := NewEndpoint(contract, func(context.Context, invokeRequest) (invokeResponse, error) {
		return invokeResponse{Value: "kernel"}, nil
	})
	if err != nil {
		t.Fatalf("NewEndpoint: %v", err)
	}
	binding, err := NewBinding(BindingOptions{
		Kind:             BindingKindIntrinsic,
		ModuleBuild:      mustModuleBuild(t, "github.com/plystra/kernel", "v0.1.0", ""),
		SelectionReason:  SelectionReasonIntrinsic,
		ContractDigest:   sha256.Sum256([]byte("kernel.info/v1 schema")),
		ConcurrencyLimit: 256,
	}, endpoint)
	if err != nil {
		t.Fatalf("NewBinding: %v", err)
	}
	dispatcher := newInvokeDispatcher(t, time.Second)
	catalog, err := NewCatalog([]Binding{binding})
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	if err := dispatcher.Publish(catalog); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	handle, root := newInvokeHandleAndContext(t, dispatcher, contract)
	response, err := handle.Invoke(root, invokeRequest{})
	if err != nil || response.Value != "kernel" {
		t.Fatalf("Invoke = %#v, %v", response, err)
	}
}

func TestHandleInvokePropagatesNestedAncestry(t *testing.T) {
	t.Parallel()

	outerContract := capability.MustParseContract[invokeRequest, invokeResponse]("example.outer/v1")
	innerContract := capability.MustParseContract[invokeRequest, invokeResponse]("example.inner/v1")
	dispatcher, err := NewDispatcher(DispatcherOptions{DefaultTimeout: time.Second})
	if err != nil {
		t.Fatalf("NewDispatcher: %v", err)
	}

	innerHandle, err := NewHandle(dispatcher, innerContract, true)
	if err != nil {
		t.Fatalf("NewHandle(inner): %v", err)
	}
	var outerContext InvocationContext
	var innerContext InvocationContext
	innerBinding := newInvokeBinding(t, innerContract, func(ctx context.Context, request invokeRequest) (invokeResponse, error) {
		var exists bool
		innerContext, exists = Current(ctx)
		if !exists {
			t.Fatal("inner provider did not receive invocation context")
		}
		return invokeResponse{Value: "inner:" + request.Value}, nil
	})
	outerBinding := newInvokeBinding(t, outerContract, func(ctx context.Context, request invokeRequest) (invokeResponse, error) {
		var exists bool
		outerContext, exists = Current(ctx)
		if !exists {
			t.Fatal("outer provider did not receive invocation context")
		}
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

	outerHandle, err := NewHandle(dispatcher, outerContract, true)
	if err != nil {
		t.Fatalf("NewHandle(outer): %v", err)
	}
	root := context.Background()
	response, err := outerHandle.Invoke(root, invokeRequest{Value: "request"})
	if err != nil || response.Value != "outer:inner:request" {
		t.Fatalf("outer Invoke = %#v, %v", response, err)
	}

	if !outerContext.InvocationID().Valid() || !innerContext.InvocationID().Valid() ||
		innerContext.ParentInvocationID() != outerContext.InvocationID() || outerContext.ParentInvocationID().Valid() ||
		innerContext.RequestID() != outerContext.RequestID() || innerContext.TraceID() != outerContext.TraceID() {
		t.Fatalf("nested contexts = outer %#v / inner %#v", outerContext, innerContext)
	}
}

func TestHandleInvokeTreatsDroppedContextAsIndependentCall(t *testing.T) {
	t.Parallel()

	outerContract := capability.MustParseContract[invokeRequest, invokeResponse]("example.context-outer/v1")
	innerContract := capability.MustParseContract[invokeRequest, invokeResponse]("example.context-inner/v1")
	dispatcher := newInvokeDispatcher(t, time.Second)
	innerHandle, err := NewHandle(dispatcher, innerContract, true)
	if err != nil {
		t.Fatalf("NewHandle(inner): %v", err)
	}
	var innerProviderCalls atomic.Int32
	var outerContext InvocationContext
	var innerContext InvocationContext
	innerBinding := newInvokeBinding(t, innerContract, func(ctx context.Context, request invokeRequest) (invokeResponse, error) {
		innerProviderCalls.Add(1)
		innerContext, _ = Current(ctx)
		return invokeResponse{Value: "inner:" + request.Value}, nil
	})
	outerBinding := newInvokeBinding(t, outerContract, func(ctx context.Context, request invokeRequest) (invokeResponse, error) {
		outerContext, _ = Current(ctx)
		return innerHandle.Invoke(context.Background(), request)
	})
	catalog, err := NewCatalog([]Binding{outerBinding, innerBinding})
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	if err := dispatcher.Publish(catalog); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	outerHandle, root := newInvokeHandleAndContext(t, dispatcher, outerContract)
	response, err := outerHandle.Invoke(root, invokeRequest{Value: "request"})
	if err != nil || response.Value != "inner:request" || innerProviderCalls.Load() != 1 {
		t.Fatalf("independent nested context = %#v, %v, inner provider calls %d", response, err, innerProviderCalls.Load())
	}
	if !outerContext.InvocationID().Valid() || !innerContext.InvocationID().Valid() || innerContext.ParentInvocationID().Valid() ||
		innerContext.RequestID() == outerContext.RequestID() || innerContext.TraceID() == outerContext.TraceID() {
		t.Fatalf("independent contexts = outer %#v / inner %#v", outerContext, innerContext)
	}
}

func TestHandleInvokeIsSafeForConcurrentRootCalls(t *testing.T) {
	t.Parallel()

	contract := capability.MustParseContract[invokeRequest, invokeResponse]("example.concurrent-invoke/v1")
	contexts := make(chan InvocationContext, 128)
	harness := newInvokeHarness(t, contract, func(ctx context.Context, request invokeRequest) (invokeResponse, error) {
		current, exists := Current(ctx)
		if !exists {
			t.Error("provider did not receive invocation context")
			return invokeResponse{}, errors.New("missing invocation context")
		}
		contexts <- current
		return invokeResponse(request), nil
	}, invokeHarnessOptions{})

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

	close(contexts)
	requests := make(map[RequestID]struct{}, calls)
	traces := make(map[TraceID]struct{}, calls)
	invocations := make(map[InvocationID]struct{}, calls)
	for current := range contexts {
		if !current.RequestID().Valid() || !current.TraceID().Valid() || !current.InvocationID().Valid() || current.ParentInvocationID().Valid() {
			t.Fatalf("concurrent context = %#v", current)
		}
		requests[current.RequestID()] = struct{}{}
		traces[current.TraceID()] = struct{}{}
		invocations[current.InvocationID()] = struct{}{}
	}
	if len(requests) != calls || len(traces) != calls || len(invocations) != calls {
		t.Fatalf("unique request/trace/invocation IDs = %d/%d/%d, want %d", len(requests), len(traces), len(invocations), calls)
	}
}

func TestHandleInvokeNormalizesProviderFailuresAndPanics(t *testing.T) {
	t.Parallel()

	safe, err := NewError(ErrorInvalidArgument, "contract.invalid_request")
	if err != nil {
		t.Fatalf("NewError: %v", err)
	}
	unknownCause, err := NewError(ErrorUnavailable, "transport.delivery_unknown")
	if err != nil {
		t.Fatalf("NewError(result unknown): %v", err)
	}
	for _, test := range []struct {
		name       string
		handler    capability.Handler[invokeRequest, invokeResponse]
		code       ErrorCode
		detail     string
		wantShared *Error
	}{
		{name: "safe error", handler: func(context.Context, invokeRequest) (invokeResponse, error) {
			return invokeResponse{Value: "must not escape"}, safe
		}, code: ErrorInvalidArgument, detail: "contract.invalid_request", wantShared: safe},
		{name: "wrapped safe error", handler: func(context.Context, invokeRequest) (invokeResponse, error) {
			return invokeResponse{}, fmt.Errorf("provider password=secret: %w", safe)
		}, code: ErrorInvalidArgument, detail: "contract.invalid_request", wantShared: safe},
		{name: "result unknown", handler: func(context.Context, invokeRequest) (invokeResponse, error) {
			return invokeResponse{}, NewResultUnknown(unknownCause)
		}, code: ErrorUnavailable, detail: "transport.delivery_unknown"},
		{name: "raw sensitive error", handler: func(context.Context, invokeRequest) (invokeResponse, error) {
			return invokeResponse{}, errors.New("provider password=secret")
		}, code: ErrorInternal, detail: detailProviderFailed},
		{name: "undeclared semantic error", handler: func(context.Context, invokeRequest) (invokeResponse, error) {
			return invokeResponse{}, endpointSemanticError("invalid_recipient")
		}, code: ErrorInternal, detail: detailProviderFailed},
		{name: "malformed semantic error", handler: func(context.Context, invokeRequest) (invokeResponse, error) {
			return invokeResponse{}, endpointSemanticError("InvalidRecipient")
		}, code: ErrorInternal, detail: detailProviderFailed},
		{name: "semantic code panic", handler: func(context.Context, invokeRequest) (invokeResponse, error) {
			return invokeResponse{}, endpointPanickingSemanticError{}
		}, code: ErrorInternal, detail: detailProviderFailed},
		{name: "normalizer panic", handler: func(context.Context, invokeRequest) (invokeResponse, error) {
			return invokeResponse{}, panickingProviderError{}
		}, code: ErrorInternal, detail: detailProviderFailed},
		{name: "panic", handler: func(context.Context, invokeRequest) (invokeResponse, error) {
			panic("provider token=secret")
		}, code: ErrorInternal, detail: detailProviderPanic},
		{name: "deadline error", handler: func(context.Context, invokeRequest) (invokeResponse, error) {
			return invokeResponse{}, context.DeadlineExceeded
		}, code: ErrorTimeout, detail: detailDeadlineExceeded},
		{name: "cancelled error", handler: func(context.Context, invokeRequest) (invokeResponse, error) {
			return invokeResponse{}, context.Canceled
		}, code: ErrorCancelled, detail: detailInvocationCancelled},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			contract := capability.MustParseContract[invokeRequest, invokeResponse]("example.provider-failure/v1")
			harness := newInvokeHarness(t, contract, test.handler, invokeHarnessOptions{})
			response, err := harness.handle.Invoke(harness.root, invokeRequest{})
			boundary := requireInvocationError(t, err, test.code, test.detail)
			if response != (invokeResponse{}) || strings.Contains(err.Error(), "secret") {
				t.Fatalf("provider failure = %#v, %v", response, err)
			}
			if test.wantShared != nil && boundary != test.wantShared {
				t.Fatalf("safe boundary copy = %#v, want original %#v", boundary, test.wantShared)
			}
		})
	}
}

func TestHandleInvokePreservesDeclaredSemanticError(t *testing.T) {
	t.Parallel()

	contract := capability.MustParseContractWithSemanticErrors[invokeRequest, invokeResponse](
		"example.semantic-invoke/v1",
		"invalid_recipient",
		"temporarily_unavailable",
	)
	harness := newInvokeHarness(t, contract, func(context.Context, invokeRequest) (invokeResponse, error) {
		return invokeResponse{Value: "must not escape"}, fmt.Errorf(
			"provider password=secret: %w",
			endpointSemanticError("invalid_recipient"),
		)
	}, invokeHarnessOptions{})

	response, err := harness.handle.Invoke(harness.root, invokeRequest{})
	if semantic, ok := errors.AsType[*SemanticError](err); !ok || !semantic.valid() || semantic.Code() != "invalid_recipient" {
		t.Fatalf("semantic error = %#v / %v", semantic, err)
	}
	if response != (invokeResponse{}) || strings.Contains(err.Error(), "password") || strings.Contains(err.Error(), "secret") {
		t.Fatalf("semantic Invoke = %#v, %v", response, err)
	}
}

func TestHandleInvokeAppliesDeadlineToProvider(t *testing.T) {
	t.Parallel()

	contract := capability.MustParseContract[invokeRequest, invokeResponse]("example.deadline/v1")
	var providerCalls atomic.Int32
	harness := newInvokeHarness(t, contract, func(ctx context.Context, _ invokeRequest) (invokeResponse, error) {
		providerCalls.Add(1)
		<-ctx.Done()
		return invokeResponse{Value: "late success"}, nil
	}, invokeHarnessOptions{defaultTimeout: 15 * time.Millisecond})

	response, err := harness.handle.Invoke(harness.root, invokeRequest{})
	requireInvocationError(t, err, ErrorTimeout, detailDeadlineExceeded)
	if !errors.Is(err, context.DeadlineExceeded) || response != (invokeResponse{}) || providerCalls.Load() != 1 {
		t.Fatalf("deadline Invoke = %#v, %v, provider calls %d", response, err, providerCalls.Load())
	}
}

func TestHandleInvokePropagatesCancellation(t *testing.T) {
	t.Parallel()

	parent, cancel := context.WithCancel(context.Background())
	contract := capability.MustParseContract[invokeRequest, invokeResponse]("example.cancel/v1")
	harness := newInvokeHarness(t, contract, func(ctx context.Context, _ invokeRequest) (invokeResponse, error) {
		cancel()
		<-ctx.Done()
		return invokeResponse{}, ctx.Err()
	}, invokeHarnessOptions{parent: parent})

	response, err := harness.handle.Invoke(harness.root, invokeRequest{})
	requireInvocationError(t, err, ErrorCancelled, detailInvocationCancelled)
	if !errors.Is(err, context.Canceled) || response != (invokeResponse{}) {
		t.Fatalf("cancelled Invoke = %#v, %v", response, err)
	}
}

func TestHandleInvokeRejectsPreCancelledContext(t *testing.T) {
	t.Parallel()

	parent, cancel := context.WithCancel(context.Background())
	contract := capability.MustParseContract[invokeRequest, invokeResponse]("example.pre-cancel/v1")
	var providerCalls atomic.Int32
	harness := newInvokeHarness(t, contract, func(context.Context, invokeRequest) (invokeResponse, error) {
		providerCalls.Add(1)
		return invokeResponse{}, nil
	}, invokeHarnessOptions{parent: parent})
	cancel()

	response, err := harness.handle.Invoke(harness.root, invokeRequest{})
	requireInvocationError(t, err, ErrorCancelled, detailInvocationCancelled)
	if !errors.Is(err, context.Canceled) || response != (invokeResponse{}) || providerCalls.Load() != 0 {
		t.Fatalf("pre-cancelled Invoke = %#v, %v, provider calls %d", response, err, providerCalls.Load())
	}
}

func TestHandleInvokeRechecksCancellationAfterFastProviderReturn(t *testing.T) {
	t.Parallel()

	parent, cancel := context.WithCancel(context.Background())
	contract := capability.MustParseContract[invokeRequest, invokeResponse]("example.fast-cancel/v1")
	harness := newInvokeHarness(t, contract, func(context.Context, invokeRequest) (invokeResponse, error) {
		cancel()
		return invokeResponse{Value: "must not escape"}, nil
	}, invokeHarnessOptions{parent: parent})

	response, err := harness.handle.Invoke(harness.root, invokeRequest{})
	requireInvocationError(t, err, ErrorCancelled, detailInvocationCancelled)
	if !errors.Is(err, context.Canceled) || response != (invokeResponse{}) {
		t.Fatalf("fast cancellation Invoke = %#v, %v", response, err)
	}
}

func TestHandleInvokeClassifiesCallerDeadline(t *testing.T) {
	t.Parallel()

	parent, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	defer cancel()
	contract := capability.MustParseContract[invokeRequest, invokeResponse]("example.authority-deadline/v1")
	harness := newInvokeHarness(t, contract, func(ctx context.Context, _ invokeRequest) (invokeResponse, error) {
		<-ctx.Done()
		return invokeResponse{}, ctx.Err()
	}, invokeHarnessOptions{parent: parent, defaultTimeout: time.Second})

	response, err := harness.handle.Invoke(harness.root, invokeRequest{})
	requireInvocationError(t, err, ErrorTimeout, detailDeadlineExceeded)
	if !errors.Is(err, context.DeadlineExceeded) || response != (invokeResponse{}) {
		t.Fatalf("authority deadline Invoke = %#v, %v", response, err)
	}
}

func TestHandleInvokeRejectsContractMismatchWithoutCallingProvider(t *testing.T) {
	t.Parallel()

	providerContract := capability.MustParseContract[invokeRequest, invokeResponse]("example.contract/v1")
	callerContract := capability.MustParseContract[invokeRequest, invokeResponse]("example.contract/v1")
	var providerCalls atomic.Int32
	harness := newInvokeHarness(t, providerContract, func(context.Context, invokeRequest) (invokeResponse, error) {
		providerCalls.Add(1)
		return invokeResponse{}, nil
	}, invokeHarnessOptions{})
	handle, err := NewHandle(harness.dispatcher, callerContract, true)
	if err != nil {
		t.Fatalf("NewHandle: %v", err)
	}
	response, err := handle.Invoke(harness.root, invokeRequest{})
	boundary := requireInvocationError(t, err, ErrorInternal, detailContractMismatch)
	if boundary.Completion() != CompletionNotStarted {
		t.Fatalf("contract mismatch completion = %s", boundary.Completion())
	}
	if response != (invokeResponse{}) || providerCalls.Load() != 0 {
		t.Fatalf("contract mismatch = %#v, %v, provider calls %d", response, err, providerCalls.Load())
	}
}

func TestHandleInvokeRejectsPreEntryFailuresWithoutProviderExecution(t *testing.T) {
	t.Parallel()

	contract := capability.MustParseContract[invokeRequest, invokeResponse]("example.pre-entry/v1")
	var providerCalls atomic.Int32
	harness := newInvokeHarness(t, contract, func(context.Context, invokeRequest) (invokeResponse, error) {
		providerCalls.Add(1)
		return invokeResponse{}, nil
	}, invokeHarnessOptions{})

	var zero Handle[invokeRequest, invokeResponse]
	_, err := zero.Invoke(context.Background(), invokeRequest{})
	requireInvocationError(t, err, ErrorInternal, detailInvalidHandle)
	var nilContext context.Context
	_, err = harness.handle.Invoke(nilContext, invokeRequest{})
	requireInvocationError(t, err, ErrorInvalidArgument, detailContextRequired)
	unavailable, err := NewHandle(harness.dispatcher, contract, false)
	if err != nil {
		t.Fatalf("NewHandle(unavailable): %v", err)
	}
	_, err = unavailable.Invoke(harness.root, invokeRequest{})
	requireInvocationError(t, err, ErrorUnavailable, detailCapabilityUnavailable)

	unpublished := newInvokeDispatcher(t, time.Second)
	unpublishedHandle, unpublishedRoot := newInvokeHandleAndContext(t, unpublished, contract)
	_, err = unpublishedHandle.Invoke(unpublishedRoot, invokeRequest{})
	requireInvocationError(t, err, ErrorUnavailable, detailDispatcherNotReady)

	empty := newInvokeDispatcher(t, time.Second)
	emptyCatalog, err := NewCatalog(nil)
	if err != nil {
		t.Fatalf("NewCatalog(nil): %v", err)
	}
	if err := empty.Publish(emptyCatalog); err != nil {
		t.Fatalf("Publish(empty): %v", err)
	}
	emptyHandle, emptyRoot := newInvokeHandleAndContext(t, empty, contract)
	_, err = emptyHandle.Invoke(emptyRoot, invokeRequest{})
	requireInvocationError(t, err, ErrorUnavailable, detailCapabilityUnavailable)

	if providerCalls.Load() != 0 {
		t.Fatalf("pre-entry work reached provider: %d calls", providerCalls.Load())
	}
}

func BenchmarkKernelCanonicalDispatch(b *testing.B) {
	handle, root, _ := benchmarkInvokeRuntime(b)
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

type invokeHarnessOptions struct {
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
	if options.parent == nil {
		options.parent = context.Background()
	}
	if options.defaultTimeout == 0 {
		options.defaultTimeout = time.Second
	}
	binding := newInvokeBinding(t, contract, handler)
	dispatcher := newInvokeDispatcher(t, options.defaultTimeout)
	catalog, err := NewCatalog([]Binding{binding})
	if err != nil {
		t.Fatalf("NewCatalog: %v", err)
	}
	if err := dispatcher.Publish(catalog); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	handle, err := NewHandle(dispatcher, contract, true)
	if err != nil {
		t.Fatalf("NewHandle: %v", err)
	}
	return invokeHarness{
		dispatcher: dispatcher,
		handle:     handle,
		root:       options.parent,
		binding:    binding,
	}
}

func newInvokeDispatcher(t *testing.T, timeout time.Duration) *Dispatcher {
	t.Helper()
	dispatcher, err := NewDispatcher(DispatcherOptions{DefaultTimeout: timeout})
	if err != nil {
		t.Fatalf("NewDispatcher: %v", err)
	}
	return dispatcher
}

func newInvokeHandleAndContext(
	t *testing.T,
	dispatcher *Dispatcher,
	contract capability.Contract[invokeRequest, invokeResponse],
) (Handle[invokeRequest, invokeResponse], context.Context) {
	t.Helper()
	handle, err := NewHandle(dispatcher, contract, true)
	if err != nil {
		t.Fatalf("NewHandle: %v", err)
	}
	return handle, context.Background()
}

func newInvokeBinding(
	t *testing.T,
	contract capability.Contract[invokeRequest, invokeResponse],
	handler capability.Handler[invokeRequest, invokeResponse],
) Binding {
	t.Helper()
	endpoint, err := NewEndpoint(contract, handler)
	if err != nil {
		t.Fatalf("NewEndpoint: %v", err)
	}
	binding, err := NewBinding(BindingOptions{
		Kind:             BindingKindImplementation,
		Constructor:      "github.com/acme/invoke/implementation.New",
		ModuleBuild:      mustModuleBuild(t, "github.com/acme/invoke", "v1.0.0", ""),
		SelectionReason:  SelectionReasonUniqueCompatible,
		ContractDigest:   sha256.Sum256([]byte(contract.Identifier().String() + " schema")),
		ConcurrencyLimit: 256,
	}, endpoint)
	if err != nil {
		t.Fatalf("NewBinding: %v", err)
	}
	return binding
}

func requireInvocationError(t *testing.T, err error, code ErrorCode, detail string) *Error {
	t.Helper()
	var boundary *Error
	if !errors.As(err, &boundary) || !boundary.valid() || boundary.Code() != code || boundary.DetailCode() != detail {
		t.Fatalf("invocation error = %#v / %v, want %s:%s", boundary, err, code, detail)
	}
	return boundary
}

type panickingProviderError struct{}

func (panickingProviderError) Error() string { return "provider password=secret" }

func (panickingProviderError) As(any) bool { panic("provider token=secret") }

func benchmarkInvokeRuntime(b *testing.B) (Handle[invokeRequest, invokeResponse], context.Context, Binding) {
	b.Helper()
	contract := capability.MustParseContract[invokeRequest, invokeResponse]("example.benchmark-invoke/v1")
	endpoint, err := NewEndpoint(contract, func(_ context.Context, request invokeRequest) (invokeResponse, error) {
		return invokeResponse(request), nil
	})
	if err != nil {
		b.Fatalf("NewEndpoint: %v", err)
	}
	binding, err := NewBinding(BindingOptions{
		Kind:             BindingKindImplementation,
		Constructor:      "github.com/acme/benchmark/implementation.New",
		ModuleBuild:      mustModuleBuildBenchmark(b, "github.com/acme/benchmark", "v1.0.0", ""),
		SelectionReason:  SelectionReasonUniqueCompatible,
		ContractDigest:   sha256.Sum256([]byte("example.benchmark-invoke/v1 schema")),
		ConcurrencyLimit: 256,
	}, endpoint)
	if err != nil {
		b.Fatalf("NewBinding: %v", err)
	}
	catalog, err := NewCatalog([]Binding{binding})
	if err != nil {
		b.Fatalf("NewCatalog: %v", err)
	}
	dispatcher, err := NewDispatcher(DispatcherOptions{DefaultTimeout: time.Second})
	if err != nil {
		b.Fatalf("NewDispatcher: %v", err)
	}
	if err := dispatcher.Publish(catalog); err != nil {
		b.Fatalf("Publish: %v", err)
	}
	handle, err := NewHandle(dispatcher, contract, true)
	if err != nil {
		b.Fatalf("NewHandle: %v", err)
	}
	return handle, context.Background(), binding
}

var benchmarkInvokeResponse invokeResponse
