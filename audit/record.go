package audit

import (
	"crypto/sha256"
	"errors"
	"time"

	"github.com/plystra/kernel/capability"
	"github.com/plystra/kernel/plugin"
)

// ErrInvalidInvocationRecord reports an incomplete or contradictory terminal
// invocation audit envelope.
var ErrInvalidInvocationRecord = errors.New("invalid runtime invocation audit record")

// ProviderKind distinguishes Kernel-owned implementations from concrete
// plugin implementations in runtime provenance.
type ProviderKind string

const (
	ProviderKindKernel ProviderKind = "kernel"
	ProviderKindPlugin ProviderKind = "plugin"
)

// String returns the stable audited provider-kind representation.
func (k ProviderKind) String() string {
	if !k.Valid() {
		return ""
	}
	return string(k)
}

// Valid reports whether the kind identifies one supported provider owner.
func (k ProviderKind) Valid() bool {
	return k == ProviderKindKernel || k == ProviderKindPlugin
}

// ExecutionClass identifies whether provider execution occurred inside the
// local process or across a remote transport boundary.
type ExecutionClass string

const (
	ExecutionLocal  ExecutionClass = "local"
	ExecutionRemote ExecutionClass = "remote"
)

// String returns the stable audited execution classification.
func (c ExecutionClass) String() string {
	if !c.Valid() {
		return ""
	}
	return string(c)
}

// Valid reports whether the execution classification is closed and known.
func (c ExecutionClass) Valid() bool {
	return c == ExecutionLocal || c == ExecutionRemote
}

// InvocationRecordOptions contains the complete terminal facts for one
// governed capability invocation. Duration is measured independently from the
// wall-clock timestamps so a monotonic runtime clock may be used.
type InvocationRecordOptions struct {
	RuntimeCaller          CallerIdentity
	Capability             capability.Identifier
	CapabilitySchemaDigest [sha256.Size]byte
	ProviderKind           ProviderKind
	ProviderPluginID       plugin.ID
	ProviderBuild          ModuleBuild
	SecurityContext        SecurityContext
	RequestID              RequestID
	TraceID                TraceID
	InvocationID           InvocationID
	ParentInvocationID     InvocationID
	ExecutionClass         ExecutionClass
	StartedAt              time.Time
	CompletedAt            time.Time
	Duration               time.Duration
	Outcome                Outcome
}

// InvocationRecord is one immutable provider-aware, domain-neutral terminal
// audit envelope. It stores no request or response payload, provider error,
// panic value, stack trace, credential, or User record.
type InvocationRecord struct {
	runtimeCaller          CallerIdentity
	capability             capability.Identifier
	capabilitySchemaDigest [sha256.Size]byte
	providerKind           ProviderKind
	providerPluginID       plugin.ID
	providerBuild          ModuleBuild
	securityContext        SecurityContext
	requestID              RequestID
	traceID                TraceID
	invocationID           InvocationID
	parentInvocationID     InvocationID
	executionClass         ExecutionClass
	startedAt              time.Time
	completedAt            time.Time
	duration               time.Duration
	outcome                Outcome
}

// NewInvocationRecord validates and copies one complete terminal invocation.
// Timestamps are normalized to UTC and stripped of monotonic clock state;
// callers should supply Duration from a monotonic elapsed-time measurement.
func NewInvocationRecord(options InvocationRecordOptions) (InvocationRecord, error) {
	startedAt, startedOK := canonicalInvocationTime(options.StartedAt)
	completedAt, completedOK := canonicalInvocationTime(options.CompletedAt)
	if !startedOK || !completedOK {
		return InvocationRecord{}, ErrInvalidInvocationRecord
	}
	record := InvocationRecord{
		runtimeCaller:          options.RuntimeCaller,
		capability:             options.Capability,
		capabilitySchemaDigest: options.CapabilitySchemaDigest,
		providerKind:           options.ProviderKind,
		providerPluginID:       options.ProviderPluginID,
		providerBuild:          options.ProviderBuild,
		securityContext:        options.SecurityContext,
		requestID:              options.RequestID,
		traceID:                options.TraceID,
		invocationID:           options.InvocationID,
		parentInvocationID:     options.ParentInvocationID,
		executionClass:         options.ExecutionClass,
		startedAt:              startedAt,
		completedAt:            completedAt,
		duration:               options.Duration,
		outcome:                options.Outcome,
	}
	if !record.Valid() {
		return InvocationRecord{}, ErrInvalidInvocationRecord
	}
	return record, nil
}

// RuntimeCaller returns the logical Kernel or plugin code provenance that
// requested this invocation. It is distinct from the caller Principal.
func (r InvocationRecord) RuntimeCaller() CallerIdentity {
	if !r.Valid() {
		return CallerIdentity{}
	}
	return r.runtimeCaller
}

// Capability returns the exact provider-independent capability identity.
func (r InvocationRecord) Capability() capability.Identifier {
	if !r.Valid() {
		return capability.Identifier{}
	}
	return r.capability
}

// CapabilitySchemaDigest returns the canonical SHA-256 schema digest.
func (r InvocationRecord) CapabilitySchemaDigest() [sha256.Size]byte {
	if !r.Valid() {
		return [sha256.Size]byte{}
	}
	return r.capabilitySchemaDigest
}

// ProviderKind returns whether the selected implementation is Kernel-owned or
// supplied by a concrete plugin.
func (r InvocationRecord) ProviderKind() ProviderKind {
	if !r.Valid() {
		return ""
	}
	return r.providerKind
}

// ProviderPluginID returns the selected concrete Plugin ID, or zero for a
// Kernel-owned implementation.
func (r InvocationRecord) ProviderPluginID() plugin.ID {
	if !r.Valid() {
		return plugin.ID{}
	}
	return r.providerPluginID
}

// ProviderBuild returns the selected implementation's Go module provenance.
func (r InvocationRecord) ProviderBuild() ModuleBuild {
	if !r.Valid() {
		return ModuleBuild{}
	}
	return r.providerBuild
}

// SecurityContext returns the provider-neutral caller and subject Principals
// plus optional opaque authentication and authorization references.
func (r InvocationRecord) SecurityContext() SecurityContext {
	if !r.Valid() {
		return SecurityContext{}
	}
	return r.securityContext
}

// RequestID returns the root request identity.
func (r InvocationRecord) RequestID() RequestID {
	if !r.Valid() {
		return RequestID{}
	}
	return r.requestID
}

// TraceID returns the invocation call-chain identity.
func (r InvocationRecord) TraceID() TraceID {
	if !r.Valid() {
		return TraceID{}
	}
	return r.traceID
}

// InvocationID returns this invocation's unique identity.
func (r InvocationRecord) InvocationID() InvocationID {
	if !r.Valid() {
		return InvocationID{}
	}
	return r.invocationID
}

// ParentInvocationID returns the immediate parent, or zero for the first
// capability invocation in a request.
func (r InvocationRecord) ParentInvocationID() InvocationID {
	if !r.Valid() {
		return InvocationID{}
	}
	return r.parentInvocationID
}

// ExecutionClass returns whether provider execution was local or remote.
func (r InvocationRecord) ExecutionClass() ExecutionClass {
	if !r.Valid() {
		return ""
	}
	return r.executionClass
}

// StartedAt returns the canonical UTC invocation start time.
func (r InvocationRecord) StartedAt() time.Time {
	if !r.Valid() {
		return time.Time{}
	}
	return r.startedAt
}

// CompletedAt returns the canonical UTC terminal time.
func (r InvocationRecord) CompletedAt() time.Time {
	if !r.Valid() {
		return time.Time{}
	}
	return r.completedAt
}

// Duration returns the independently measured non-negative elapsed time.
func (r InvocationRecord) Duration() time.Duration {
	if !r.Valid() {
		return 0
	}
	return r.duration
}

// Outcome returns the safe closed terminal invocation result.
func (r InvocationRecord) Outcome() Outcome {
	if !r.Valid() {
		return Outcome{}
	}
	return r.outcome
}

// Valid reports whether the record contains one complete, non-contradictory,
// canonical terminal invocation envelope.
func (r InvocationRecord) Valid() bool {
	return r.runtimeCaller.Valid() && r.capability.String() != "" &&
		r.capabilitySchemaDigest != [sha256.Size]byte{} && r.validProvider() &&
		r.providerBuild.Valid() && r.securityContext.Valid() &&
		r.requestID.Valid() && r.traceID.Valid() && r.invocationID.Valid() &&
		(r.parentInvocationID == (InvocationID{}) ||
			r.parentInvocationID.Valid() && r.parentInvocationID != r.invocationID) &&
		r.executionClass.Valid() && canonicalStoredInvocationTime(r.startedAt) &&
		canonicalStoredInvocationTime(r.completedAt) && !r.completedAt.Before(r.startedAt) &&
		r.duration >= 0 && r.outcome.Valid()
}

func (r InvocationRecord) validProvider() bool {
	switch r.providerKind {
	case ProviderKindKernel:
		return r.providerPluginID.String() == ""
	case ProviderKindPlugin:
		return r.providerPluginID.String() != ""
	default:
		return false
	}
}

func canonicalInvocationTime(value time.Time) (time.Time, bool) {
	year := value.Year()
	if value.IsZero() || year < 1 || year > 9999 {
		return time.Time{}, false
	}
	return value.Round(0).UTC(), true
}

func canonicalStoredInvocationTime(value time.Time) bool {
	year := value.Year()
	return !value.IsZero() && year >= 1 && year <= 9999 &&
		value.Location() == time.UTC && value == value.Round(0)
}
