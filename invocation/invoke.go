package invocation

import (
	"context"
	"errors"
	"time"

	"github.com/plystra/kernel/audit"
)

const (
	detailInvalidHandle           = "runtime.invalid_handle"
	detailGovernedContextRequired = "runtime.governed_context_required"
	detailDispatcherNotReady      = "runtime.dispatcher_not_ready"
	detailCapabilityUnavailable   = "runtime.capability_unavailable"
	detailInvocationIDFailed      = "runtime.invocation_id_failed"
	detailAuthorizationFailed     = "authorization.evaluation_failed"
	detailDeadlineExceeded        = "runtime.deadline_exceeded"
	detailInvocationCancelled     = "runtime.cancelled"
	detailProviderPanic           = "provider.panic_recovered"
	detailContractMismatch        = "runtime.contract_mismatch"
	detailInvalidEndpoint         = "runtime.invalid_endpoint"
	detailProviderFailed          = "provider.failed"
	detailAuditRecordInvalid      = "audit.record_invalid"
	detailAuditRecordingFailed    = "audit.recording_failed"
	detailErrorNormalization      = "runtime.error_normalization_failed"
)

// Invoke executes one exact capability through the Kernel's mandatory
// governance boundary. Every entered invocation is authorized, deadline-bound,
// normalized to safe errors, and terminally audited before a result is exposed.
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

	startedInstant := time.Now()
	response, boundary, executionBegan := invokeGoverned(callContext, h, binding, request)
	completedInstant := time.Now()
	record, err := newInvocationAuditRecord(h, binding, callContext, startedInstant, completedInstant, boundary)
	if err != nil {
		return zero, newInvocationBoundary(audit.ErrorInternal, detailAuditRecordInvalid)
	}
	if err := h.scope.dispatcher.auditRecorder.Record(record); err != nil {
		if executionBegan {
			return zero, newInvocationBoundary(audit.ErrorResultUnknown, detailAuditRecordingFailed)
		}
		return zero, newInvocationBoundary(audit.ErrorUnavailable, detailAuditRecordingFailed)
	}
	if boundary != nil {
		return zero, boundary
	}
	return response, nil
}

func invokeGoverned[Request, Response any](
	ctx context.Context,
	handle Handle[Request, Response],
	binding Binding,
	request Request,
) (Response, *Error, bool) {
	var zero Response
	if boundary := invocationContextError(ctx); boundary != nil {
		return zero, boundary, false
	}
	authorizationRequest, err := newAuthorizationRequest(ctx, handle.scope, handle.definition.Identifier())
	if err != nil {
		return zero, newInvocationBoundary(audit.ErrorInternal, detailAuthorizationFailed), false
	}
	decision, err := authorizeCapability(ctx, handle.scope.dispatcher.authorizer, authorizationRequest)
	if boundary := invocationContextError(ctx); boundary != nil {
		return zero, boundary, false
	}
	if err != nil {
		return zero, newInvocationBoundary(audit.ErrorInternal, detailAuthorizationFailed), false
	}
	if !decision.Allowed() {
		return zero, newInvocationBoundary(audit.ErrorDenied, decision.DenialCode()), false
	}
	if binding.endpoint.definition != handle.definition {
		return zero, newInvocationBoundary(audit.ErrorInternal, detailContractMismatch), false
	}

	response, err := invokeEndpoint[Request, Response](ctx, binding.endpoint, handle.definition, request)
	if boundary := invocationContextError(ctx); boundary != nil {
		return zero, boundary, true
	}
	if err != nil {
		return zero, normalizeProviderError(err), true
	}
	return response, nil, true
}

func newInvocationAuditRecord[Request, Response any](
	handle Handle[Request, Response],
	binding Binding,
	ctx context.Context,
	startedInstant time.Time,
	completedInstant time.Time,
	boundary *Error,
) (audit.InvocationRecord, error) {
	current, exists := Current(ctx)
	if !exists {
		return audit.InvocationRecord{}, audit.ErrInvalidInvocationRecord
	}
	startedAt := startedInstant.Round(0)
	completedAt := completedInstant.Round(0)
	if completedAt.Before(startedAt) {
		completedAt = startedAt
	}
	duration := completedInstant.Sub(startedInstant)
	if duration < 0 {
		duration = 0
	}
	return audit.NewInvocationRecord(audit.InvocationRecordOptions{
		RuntimeCaller:          handle.scope.caller,
		Capability:             handle.definition.Identifier(),
		CapabilitySchemaDigest: binding.schemaDigest,
		ProviderKind:           binding.providerKind,
		ProviderPluginID:       binding.providerID,
		ProviderBuild:          binding.providerBuild,
		SecurityContext:        current.SecurityContext(),
		RequestID:              current.RequestID(),
		TraceID:                current.TraceID(),
		InvocationID:           current.InvocationID(),
		ParentInvocationID:     current.ParentInvocationID(),
		ExecutionClass:         audit.ExecutionLocal,
		StartedAt:              startedAt,
		CompletedAt:            completedAt,
		Duration:               duration,
		Outcome:                invocationOutcome(boundary),
	})
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

func invocationOutcome(boundary *Error) audit.Outcome {
	if boundary == nil {
		return audit.NewSucceededOutcome()
	}
	outcome, err := audit.NewErrorOutcome(boundary.Code(), boundary.DetailCode())
	if err == nil {
		return outcome
	}
	outcome, _ = audit.NewErrorOutcome(audit.ErrorInternal, detailErrorNormalization)
	return outcome
}

func newInvocationBoundary(code audit.ErrorCode, detailCode string) *Error {
	boundary, err := NewError(code, detailCode)
	if err == nil {
		return boundary
	}
	return &Error{code: audit.ErrorInternal, detailCode: detailErrorNormalization}
}
