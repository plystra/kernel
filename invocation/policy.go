package invocation

import (
	"errors"
	"time"
)

const (
	// PolicySchemaVersion is the closed compiled-policy layout accepted here.
	PolicySchemaVersion = 1
	// PolicyCompilerVersion identifies the compatible compiler protocol, not a CLI release.
	PolicyCompilerVersion = 1
	// PolicyDefaultsVersion identifies the absence-default contract: no added
	// deadline, limit 64, no queue, one attempt, and no circuit.
	PolicyDefaultsVersion = 1
	// DefaultConcurrencyLimit is the compiler's advertised absence default.
	DefaultConcurrencyLimit = 64
	// MaximumRetryAttempts bounds all attempts, including the first.
	MaximumRetryAttempts = 16
	// MaximumPolicyDuration is the greatest positive Go duration.
	MaximumPolicyDuration = time.Duration(1<<63 - 1)
	// RetryReplaySafe is the explicit compiled assertion required for retries.
	RetryReplaySafe = "replay_safe"
)

// ErrInvalidPolicy reports incompatible, unsupported, or contradictory compiled
// policy. It never includes the supplied values.
var ErrInvalidPolicy = errors.New("invalid compiled invocation policy")

// Policy is a fully resolved build-time input. Binding construction copies it;
// no runtime defaulting or policy parsing occurs. Unknown Go fields cannot
// compile, and unknown protocol versions or unsupported stages fail closed.
type Policy struct {
	SchemaVersion    int
	CompilerVersion  int
	DefaultsVersion  int
	Timeout          time.Duration
	ConcurrencyLimit int
	QueueLimit       int
	Retry            RetryPolicy
	Circuit          CircuitPolicy
}

// RetryPolicy requires explicit replay safety and a positive total timeout when
// MaxAttempts exceeds one. The disabled form is exactly MaxAttempts: 1.
type RetryPolicy struct {
	Eligibility string
	MaxAttempts int
	Backoff     time.Duration
}

// CircuitPolicy reserves the closed compiled fields. Only its zero (disabled)
// value is currently executable; no enabled circuit is silently ignored.
type CircuitPolicy struct {
	FailureThreshold int
	OpenFor          time.Duration
	ProbeLimit       int
}

func (p Policy) valid() bool {
	if p.SchemaVersion != PolicySchemaVersion || p.CompilerVersion != PolicyCompilerVersion || p.DefaultsVersion != PolicyDefaultsVersion ||
		p.Timeout < 0 || p.ConcurrencyLimit < 1 || p.ConcurrencyLimit > MaximumConcurrencyLimit || p.QueueLimit != 0 || p.Circuit != (CircuitPolicy{}) {
		return false
	}
	r := p.Retry
	if r.MaxAttempts == 1 {
		return r.Eligibility == "" && r.Backoff == 0
	}
	return r.Eligibility == RetryReplaySafe && r.MaxAttempts >= 2 && r.MaxAttempts <= MaximumRetryAttempts && r.Backoff >= 0 && p.Timeout > 0
}
