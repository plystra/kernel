package invocation

import "time"

func testPolicy(timeout time.Duration, limit int) Policy {
	return Policy{SchemaVersion: PolicySchemaVersion, CompilerVersion: PolicyCompilerVersion, DefaultsVersion: PolicyDefaultsVersion,
		Timeout: timeout, ConcurrencyLimit: limit, Retry: RetryPolicy{MaxAttempts: 1}}
}
