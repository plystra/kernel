package invocation

import (
	"context"

	"github.com/plystra/kernel/capability"
)

func normalizeEndpointError(definition capability.Definition, providerError error) error {
	var primary *Error
	var semanticCode string
	invalid, unknown := false, false
	addPrimary := func(boundary *Error) {
		if primary != nil && (primary.code != boundary.code || primary.detailCode != boundary.detailCode) {
			invalid = true
		}
		primary = boundary
	}
	complete := walkErrorTree(providerError, func(node error) {
		switch value := node.(type) {
		case *ResultUnknownError:
			unknown = true
		case *SemanticError:
			if !value.valid() {
				invalid = true
				return
			}
			unknown = unknown || value.completion == CompletionResultUnknown
			if !definition.DeclaresSemanticError(value.code) || (semanticCode != "" && semanticCode != value.code) {
				invalid = true
			}
			semanticCode = value.code
		case *Error:
			if !value.valid() {
				invalid = true
				return
			}
			unknown = unknown || value.completion == CompletionResultUnknown
			addPrimary(value)
		default:
			switch node {
			case context.DeadlineExceeded:
				addPrimary(newInvocationBoundary(ErrorTimeout, detailDeadlineExceeded))
			case context.Canceled:
				addPrimary(newInvocationBoundary(ErrorCancelled, detailInvocationCancelled))
			case ErrProviderPanic:
				addPrimary(newInvocationBoundary(ErrorInternal, detailProviderPanic))
			case ErrContractMismatch:
				addPrimary(newInvocationBoundary(ErrorInternal, detailContractMismatch))
			case ErrInvalidEndpoint:
				addPrimary(newInvocationBoundary(ErrorInternal, detailInvalidEndpoint))
			}
		}
	})
	completion := CompletionResultKnown
	if unknown || !complete {
		completion = CompletionResultUnknown
	}
	if !complete || invalid || (primary != nil && semanticCode != "") {
		primary = newInvocationBoundary(ErrorInternal, detailProviderFailed)
	} else if semanticCode != "" {
		return &SemanticError{code: semanticCode, completion: completion}
	} else if primary == nil {
		primary = newInvocationBoundary(ErrorInternal, detailProviderFailed)
	}
	if primary.completion == completion {
		return primary
	}
	// A nested not_started result cannot assert that this outer target never
	// entered concrete code. Uncertainty, unlike non-entry, always propagates.
	boundary := *primary
	boundary.completion = completion
	return &boundary
}

func normalizeProviderError(providerError error) error {
	// Endpoint errors are already validated and cause-free. The remaining
	// errors originate in invokeEndpoint's contract and panic checks.
	switch boundary := providerError.(type) {
	case *Error:
		if boundary.valid() {
			return boundary
		}
	case *SemanticError:
		if boundary.valid() && boundary.cause == nil {
			return boundary
		}
	}
	return normalizeEndpointError(capability.Definition{}, providerError)
}

func normalizeResponseError(err error) *Error {
	// A response check cannot manufacture a declared semantic outcome or blame
	// a caller for invalid target output. Preserve only safe internal details.
	boundary := normalizeEndpointError(capability.Definition{}, err).(*Error)
	if boundary.code != ErrorInternal || boundary.detailCode == detailProviderFailed {
		return &Error{code: ErrorInternal, detailCode: detailResponseProcessingFailed, completion: boundary.completion}
	}
	return boundary
}
