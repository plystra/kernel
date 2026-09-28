package invocation

import (
	"context"
	"time"

	"github.com/plystra/kernel/capability"
)

const (
	detailInvalidHandle             = "runtime.invalid_handle"
	detailContextRequired           = "runtime.context_required"
	detailDispatcherNotReady        = "runtime.dispatcher_not_ready"
	detailCapabilityUnavailable     = "runtime.capability_unavailable"
	detailInvocationIDFailed        = "runtime.invocation_id_failed"
	detailDeadlineExceeded          = "runtime.deadline_exceeded"
	detailInvocationCancelled       = "runtime.cancelled"
	detailProviderPanic             = "provider.panic_recovered"
	detailContractMismatch          = "runtime.contract_mismatch"
	detailInvalidEndpoint           = "runtime.invalid_endpoint"
	detailProviderFailed            = "provider.failed"
	detailErrorNormalization        = "runtime.error_normalization_failed"
	detailDispatcherDraining        = "runtime.dispatcher_draining"
	detailTargetExited              = "provider.exited_without_result"
	detailResponseProcessorRequired = "runtime.response_processor_required"
	detailResponseProcessingFailed  = "runtime.response_processing_failed"
	detailConcurrencyExhausted      = "runtime.concurrency_exhausted"
	detailRequestPreparationFailed  = "runtime.request_preparation_failed"
)

// Invoke executes one exact canonical capability through raw Kernel dispatch.
// Every invocation executes the exact binding's compiled policy and normalizes
// failures to safe errors. A policy without timeout adds no deadline.
// Cancellation may complete the caller before the target terminates. Generated
// proxies and adapters own request isolation; raw callers must not share mutable
// request storage with an executing target, including after caller completion.
func (h Handle[Request, Response]) Invoke(ctx context.Context, request Request) (Response, error) {
	return h.invoke(ctx, request, nil, nil)
}

// InvokeWithResponse runs a generated response validator and copier inside the
// tracked attempt, after a successful target result and before releasing its
// lifetime. Drain therefore cannot permit dependency cleanup while process is
// reading storage retained by the target. The processor must be non-nil, bounded,
// side-effect-free, and return a graph with no mutable aliases to target storage.
// It must not retain that storage or start asynchronous processing.
//
// Processor errors are normalized as internal failures, preserving valid safe
// internal detail codes and result uncertainty but removing private causes.
// Cancellation may complete the caller while processing continues; its late
// result is discarded and the attempt remains registered until processing ends.
func (h Handle[Request, Response]) InvokeWithResponse(ctx context.Context, request Request, process func(Response) (Response, error)) (Response, error) {
	if process == nil {
		var zero Response
		return zero, newNotStartedBoundary(ErrorInvalidArgument, detailResponseProcessorRequired)
	}
	return h.invoke(ctx, request, nil, process)
}

// InvokeWithPreparation starts the logical-call budget before a generated
// request validator/copier creates the immutable snapshot. prepare runs once,
// synchronously, and must be bounded and side-effect-free. Each endpoint adapter
// must copy that snapshot again before target entry, including on retries.
// process has the same tracked ownership contract as InvokeWithResponse.
func (h Handle[Request, Response]) InvokeWithPreparation(ctx context.Context, request Request, prepare func(Request) (Request, error), process func(Response) (Response, error)) (Response, error) {
	if prepare == nil || process == nil {
		var zero Response
		return zero, newNotStartedBoundary(ErrorInvalidArgument, detailRequestPreparationFailed)
	}
	return h.invoke(ctx, request, prepare, process)
}

func (h Handle[Request, Response]) invoke(ctx context.Context, request Request, prepare func(Request) (Request, error), process func(Response) (Response, error)) (Response, error) {
	started := time.Now()
	var zero Response
	if !h.valid() {
		return zero, newNotStartedBoundary(ErrorInternal, detailInvalidHandle)
	}
	if ctx == nil {
		return zero, newNotStartedBoundary(ErrorInvalidArgument, detailContextRequired)
	}
	if !h.available {
		return zero, newNotStartedBoundary(ErrorUnavailable, detailCapabilityUnavailable)
	}

	state, err := h.dispatcher.snapshot()
	if err != nil {
		return zero, newNotStartedBoundary(ErrorUnavailable, detailDispatcherNotReady)
	}
	binding, exists := state.entries[h.definition.Identifier()]
	if !exists {
		return zero, newNotStartedBoundary(ErrorUnavailable, detailCapabilityUnavailable)
	}

	invocationID, err := NewInvocationID()
	if err != nil {
		return zero, newNotStartedBoundary(ErrorInternal, detailInvocationIDFailed)
	}
	callContext, cleanup, err := enterInvocationContext(ctx, invocationID, binding.policy.Timeout, started)
	if err != nil {
		return zero, newNotStartedBoundary(ErrorInvalidArgument, detailContextRequired)
	}
	defer cleanup()

	if boundary := invocationContextError(callContext); boundary != nil {
		return zero, boundary
	}
	if prepare != nil {
		request, err = prepareRequest(request, prepare)
		if boundary := invocationContextError(callContext); boundary != nil {
			return zero, boundary
		}
		if err != nil {
			return zero, err
		}
	}
	return invokePolicy(callContext, cleanup, h, binding, request, process)
}

func prepareRequest[Request any](request Request, prepare func(Request) (Request, error)) (snapshot Request, err error) {
	defer func() {
		if recover() != nil {
			var zero Request
			snapshot = zero
			err = newNotStartedBoundary(ErrorInternal, detailRequestPreparationFailed)
		}
	}()
	snapshot, err = prepare(request)
	if err != nil {
		boundary := normalizeEndpointError(capability.Definition{}, err).(*Error)
		if boundary.code != ErrorInvalidArgument {
			boundary = newNotStartedBoundary(ErrorInternal, detailRequestPreparationFailed)
		} else {
			copy := *boundary
			boundary = &copy
			boundary.completion = CompletionNotStarted
		}
		return snapshot, boundary
	}
	return snapshot, nil
}

type targetResult[Response any] struct {
	attempt  targetAttempt
	response Response
	err      error
	done     chan struct{}
}

func invokeBounded[Request, Response any](
	ctx context.Context,
	cancel func(),
	handle Handle[Request, Response],
	binding Binding,
	request Request,
	process func(Response) (Response, error),
) (Response, error) {
	var zero Response
	if boundary := invocationContextError(ctx); boundary != nil {
		return zero, boundary
	}
	if binding.endpoint.definition != handle.definition {
		return zero, newNotStartedBoundary(ErrorInternal, detailContractMismatch)
	}

	result := &targetResult[Response]{attempt: targetAttempt{cancel: cancel}, done: make(chan struct{})}
	attempt := &result.attempt
	if boundary := handle.dispatcher.registerAttempt(ctx, attempt, binding); boundary != nil {
		return zero, boundary
	}
	go executeAttempt(ctx, handle.dispatcher, binding.endpoint, request, result, process)
	select {
	case <-result.done:
		if boundary := invocationContextError(ctx); boundary != nil {
			return zero, handle.dispatcher.abandonAttempt(attempt, boundary)
		}
		return result.response, result.err
	case <-ctx.Done():
		return zero, handle.dispatcher.abandonAttempt(attempt, invocationContextError(ctx))
	}
}

func executeAttempt[Request, Response any](ctx context.Context, dispatcher *Dispatcher, endpoint Endpoint, request Request, result *targetResult[Response], process func(Response) (Response, error)) {
	var response Response
	var err error
	returned := false
	processing := false
	attempt := &result.attempt
	defer func() {
		// Goexit runs defers without returning from the adapter. It must
		// release attempt ownership and cannot become a zero-value success.
		if recover() != nil || !returned {
			var zero Response
			response = zero
			err = newInvocationBoundary(ErrorInternal, detailTargetExited)
			if processing {
				err = newInvocationBoundary(ErrorInternal, detailResponseProcessingFailed)
			}
		}
		dispatcher.mu.Lock()
		if !attempt.abandoned {
			result.response, result.err = response, err
		}
		dispatcher.mu.Unlock()
		dispatcher.finishAttempt(attempt)
		close(result.done)
	}()
	if !dispatcher.enterAttempt(ctx, attempt) {
		boundary := invocationContextError(ctx)
		if boundary == nil {
			boundary = newNotStartedBoundary(ErrorUnavailable, detailDispatcherDraining)
		}
		err = boundary
		returned = true
		return
	}
	response, err = invokeEndpoint[Request, Response](ctx, endpoint, endpoint.definition, request)
	if err != nil {
		err = normalizeProviderError(err)
	} else if process != nil {
		boundary := invocationContextError(ctx)
		dispatcher.mu.Lock()
		abandoned := attempt.abandoned || dispatcher.draining
		dispatcher.mu.Unlock()
		if boundary != nil || abandoned {
			if boundary == nil {
				boundary = newInvocationBoundary(ErrorCancelled, detailInvocationCancelled)
			}
			boundary.completion = CompletionResultUnknown
			err = boundary
		} else {
			processing = true
			response, err = process(response)
			if err != nil {
				err = normalizeResponseError(err)
			}
		}
	}
	if err != nil {
		var zero Response
		response = zero
	}
	returned = true
}

func invocationContextError(ctx context.Context) *Error {
	if ctx == nil {
		return newNotStartedBoundary(ErrorInvalidArgument, detailContextRequired)
	}
	if frame, exists := runtimeFrameFrom(ctx); exists {
		if boundary := boundaryForContextError(frame.authority.Err()); boundary != nil {
			return boundary
		}
	}
	if boundary := boundaryForContextError(ctx.Err()); boundary != nil {
		return boundary
	}
	// Bounded synchronous preparation can use the entire budget before the
	// context timer's callback is scheduled. Never admit work in that gap.
	if deadline, bounded := ctx.Deadline(); bounded && !time.Now().Before(deadline) {
		return newNotStartedBoundary(ErrorTimeout, detailDeadlineExceeded)
	}
	return nil
}

func boundaryForContextError(err error) *Error {
	switch err {
	case context.DeadlineExceeded:
		return newNotStartedBoundary(ErrorTimeout, detailDeadlineExceeded)
	case context.Canceled:
		return newNotStartedBoundary(ErrorCancelled, detailInvocationCancelled)
	default:
		return nil
	}
}

func newInvocationBoundary(code ErrorCode, detailCode string) *Error {
	boundary, err := NewError(code, detailCode)
	if err == nil {
		return boundary
	}
	return &Error{code: ErrorInternal, detailCode: detailErrorNormalization, completion: CompletionResultKnown}
}

func newNotStartedBoundary(code ErrorCode, detailCode string) *Error {
	boundary := newInvocationBoundary(code, detailCode)
	boundary.completion = CompletionNotStarted
	return boundary
}
