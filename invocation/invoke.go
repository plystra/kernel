package invocation

import (
	"context"
	"errors"

	"github.com/plystra/kernel/audit"
)

const (
	detailInvalidHandle           = "runtime.invalid_handle"
	detailGovernedContextRequired = "runtime.governed_context_required"
	detailDispatcherNotReady      = "runtime.dispatcher_not_ready"
	detailCapabilityUnavailable   = "runtime.capability_unavailable"
	detailInvocationIDFailed      = "runtime.invocation_id_failed"
	detailDeadlineExceeded        = "runtime.deadline_exceeded"
	detailInvocationCancelled     = "runtime.cancelled"
	detailProviderPanic           = "provider.panic_recovered"
	detailContractMismatch        = "runtime.contract_mismatch"
	detailInvalidEndpoint         = "runtime.invalid_endpoint"
	detailProviderFailed          = "provider.failed"
	detailErrorNormalization      = "runtime.error_normalization_failed"
)

// Invoke executes one exact canonical capability through raw Kernel dispatch.
// Every entered invocation is deadline-bound and normalized to safe errors.
func (h Handle[Request, Response]) Invoke(ctx context.Context, request Request) (Response, error) {
	var zero Response
	if !h.valid() {
		return zero, newInvocationBoundary(audit.ErrorInternal, detailInvalidHandle)
	}
	if ctx == nil {
		return zero, newInvocationBoundary(audit.ErrorInvalidArgument, detailGovernedContextRequired)
	}
	if !h.available {
		return zero, newInvocationBoundary(audit.ErrorUnavailable, detailCapabilityUnavailable)
	}

	state, err := h.scope.dispatcher.snapshot()
	if err != nil {
		return zero, newInvocationBoundary(audit.ErrorUnavailable, detailDispatcherNotReady)
	}
	binding, exists := state.entries[h.definition.Identifier()]
	if !exists {
		return zero, newInvocationBoundary(audit.ErrorUnavailable, detailCapabilityUnavailable)
	}

	invocationID, err := audit.NewInvocationID()
	if err != nil {
		return zero, newInvocationBoundary(audit.ErrorInternal, detailInvocationIDFailed)
	}
	callContext, cleanup, err := enterInvocationContext(ctx, invocationID, h.scope.dispatcher.defaultTimeout)
	if err != nil {
		return zero, newInvocationBoundary(audit.ErrorInvalidArgument, detailGovernedContextRequired)
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
) (Response, *Error) {
	var zero Response
	if boundary := invocationContextError(ctx); boundary != nil {
		return zero, boundary
	}
	if binding.endpoint.definition != handle.definition {
		return zero, newInvocationBoundary(audit.ErrorInternal, detailContractMismatch)
	}

	response, err := invokeEndpoint[Request, Response](ctx, binding.endpoint, handle.definition, request)
	if boundary := invocationContextError(ctx); boundary != nil {
		return zero, boundary
	}
	if err != nil {
		return zero, normalizeProviderError(err)
	}
	return response, nil
}

func invocationContextError(ctx context.Context) *Error {
	if ctx == nil {
		return newInvocationBoundary(audit.ErrorInvalidArgument, detailGovernedContextRequired)
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
		return newInvocationBoundary(audit.ErrorTimeout, detailDeadlineExceeded)
	case context.Canceled:
		return newInvocationBoundary(audit.ErrorCancelled, detailInvocationCancelled)
	default:
		return nil
	}
}

func normalizeProviderError(providerError error) (boundary *Error) {
	boundary = newInvocationBoundary(audit.ErrorInternal, detailProviderFailed)
	defer func() {
		if recover() != nil {
			boundary = newInvocationBoundary(audit.ErrorInternal, detailProviderFailed)
		}
	}()
	var safe *Error
	if errors.As(providerError, &safe) && safe.valid() {
		return safe
	}
	switch {
	case errors.Is(providerError, context.DeadlineExceeded):
		return newInvocationBoundary(audit.ErrorTimeout, detailDeadlineExceeded)
	case errors.Is(providerError, context.Canceled):
		return newInvocationBoundary(audit.ErrorCancelled, detailInvocationCancelled)
	case errors.Is(providerError, ErrProviderPanic):
		return newInvocationBoundary(audit.ErrorInternal, detailProviderPanic)
	case errors.Is(providerError, ErrContractMismatch):
		return newInvocationBoundary(audit.ErrorInternal, detailContractMismatch)
	case errors.Is(providerError, ErrInvalidEndpoint):
		return newInvocationBoundary(audit.ErrorInternal, detailInvalidEndpoint)
	default:
		return boundary
	}
}

func newInvocationBoundary(code audit.ErrorCode, detailCode string) *Error {
	boundary, err := NewError(code, detailCode)
	if err == nil {
		return boundary
	}
	return &Error{code: audit.ErrorInternal, detailCode: detailErrorNormalization}
}
