package invocation

import "context"

const (
	detailInvalidHandle         = "runtime.invalid_handle"
	detailContextRequired       = "runtime.context_required"
	detailDispatcherNotReady    = "runtime.dispatcher_not_ready"
	detailCapabilityUnavailable = "runtime.capability_unavailable"
	detailInvocationIDFailed    = "runtime.invocation_id_failed"
	detailDeadlineExceeded      = "runtime.deadline_exceeded"
	detailInvocationCancelled   = "runtime.cancelled"
	detailProviderPanic         = "provider.panic_recovered"
	detailContractMismatch      = "runtime.contract_mismatch"
	detailInvalidEndpoint       = "runtime.invalid_endpoint"
	detailProviderFailed        = "provider.failed"
	detailErrorNormalization    = "runtime.error_normalization_failed"
)

// Invoke executes one exact canonical capability through raw Kernel dispatch.
// Every entered invocation is deadline-bound and normalized to safe errors.
func (h Handle[Request, Response]) Invoke(ctx context.Context, request Request) (Response, error) {
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
	callContext, cleanup, err := enterInvocationContext(ctx, invocationID, h.dispatcher.defaultTimeout)
	if err != nil {
		return zero, newNotStartedBoundary(ErrorInvalidArgument, detailContextRequired)
	}
	defer cleanup()

	response, boundary := invokeBounded(callContext, h, binding, request)
	if boundary != nil {
		return zero, boundary
	}
	return response, nil
}

func invokeBounded[Request, Response any](
	ctx context.Context,
	handle Handle[Request, Response],
	binding Binding,
	request Request,
) (Response, error) {
	var zero Response
	if boundary := invocationContextError(ctx); boundary != nil {
		return zero, boundary
	}
	if binding.endpoint.definition != handle.definition {
		return zero, newNotStartedBoundary(ErrorInternal, detailContractMismatch)
	}

	response, err := invokeEndpoint[Request, Response](ctx, binding.endpoint, handle.definition, request)
	if boundary := invocationContextError(ctx); boundary != nil {
		boundary.completion = CompletionResultUnknown
		return zero, boundary
	}
	if err != nil {
		return zero, normalizeProviderError(err)
	}
	return response, nil
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
	return boundaryForContextError(ctx.Err())
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
