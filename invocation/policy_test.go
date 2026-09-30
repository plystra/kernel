package invocation_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	"github.com/plystra/kernel/capability"
	"github.com/plystra/kernel/invocation"
)

var policyTestContract = capability.MustParseContractWithSemanticErrors[string, string]("example.policy/v1", "not_ready")

func publicPolicy(timeout time.Duration, limit int) invocation.Policy {
	return invocation.Policy{SchemaVersion: invocation.PolicySchemaVersion, CompilerVersion: invocation.PolicyCompilerVersion,
		DefaultsVersion: invocation.PolicyDefaultsVersion, Timeout: timeout, ConcurrencyLimit: limit,
		Retry: invocation.RetryPolicy{MaxAttempts: 1}}
}

func TestCompiledPolicyRejectsUnsupportedInputBeforePublication(t *testing.T) {
	valid := publicPolicy(0, invocation.DefaultConcurrencyLimit)
	for _, change := range []func(*invocation.Policy){
		func(p *invocation.Policy) { p.SchemaVersion = 0 },
		func(p *invocation.Policy) { p.SchemaVersion++ },
		func(p *invocation.Policy) { p.CompilerVersion++ },
		func(p *invocation.Policy) { p.DefaultsVersion++ },
		func(p *invocation.Policy) { p.Timeout = -1 },
		func(p *invocation.Policy) { p.ConcurrencyLimit = 0 },
		func(p *invocation.Policy) { p.ConcurrencyLimit = invocation.MaximumConcurrencyLimit + 1 },
		func(p *invocation.Policy) { p.QueueLimit = 1 },
		func(p *invocation.Policy) { p.Retry.MaxAttempts = 0 },
		func(p *invocation.Policy) { p.Retry.MaxAttempts = 2 },
		func(p *invocation.Policy) { p.Retry.Backoff = 1 },
		func(p *invocation.Policy) { p.Retry.Eligibility = "replay_safe" },
		func(p *invocation.Policy) { p.Circuit.FailureThreshold = 1 },
		func(p *invocation.Policy) { p.Circuit.OpenFor = 1 },
		func(p *invocation.Policy) { p.Circuit.ProbeLimit = 1 },
		func(p *invocation.Policy) {
			p.Timeout = time.Minute
			p.Retry = invocation.RetryPolicy{Eligibility: invocation.RetryReplaySafe, MaxAttempts: 17}
		},
		func(p *invocation.Policy) {
			p.Timeout = time.Minute
			p.Retry = invocation.RetryPolicy{Eligibility: "unknown", MaxAttempts: 2}
		},
		func(p *invocation.Policy) {
			p.Timeout = time.Minute
			p.Retry = invocation.RetryPolicy{Eligibility: invocation.RetryReplaySafe, MaxAttempts: 2, Backoff: -1}
		},
	} {
		policy := valid
		change(&policy)
		binding, err := policyBinding(t, policy, policyTestContract, func(context.Context, string) (string, error) { return "", nil })
		if !errors.Is(err, invocation.ErrInvalidPolicy) || !errors.Is(err, invocation.ErrInvalidBinding) || binding.Policy() != (invocation.Policy{}) {
			t.Fatalf("invalid policy accepted: %#v, %v", policy, err)
		}
		if _, err := invocation.NewCatalog([]invocation.Binding{binding}); !errors.Is(err, invocation.ErrInvalidCatalog) {
			t.Fatal("invalid policy entered catalog")
		}
	}
	binding, err := policyBinding(t, valid, policyTestContract, func(context.Context, string) (string, error) { return "", nil })
	if err != nil || binding.Policy() != valid {
		t.Fatalf("valid policy: %v", err)
	}
	copy := binding.Policy()
	copy.Retry.MaxAttempts = 16
	if binding.Policy() != valid {
		t.Fatal("policy storage was mutable")
	}
}

func FuzzCompiledPolicy(f *testing.F) {
	f.Add(1, 1, 1, int64(0), 64, 0, 1, "", int64(0), 0)
	f.Add(1, 1, 1, int64(time.Second), 1, 0, 3, invocation.RetryReplaySafe, int64(time.Millisecond), 0)
	f.Add(2, 1, 1, int64(-1), 0, 1, 17, "unknown", int64(-1), 1)
	f.Fuzz(func(t *testing.T, schema, compiler, defaults int, timeout int64, limit, queue, attempts int, eligibility string, backoff int64, circuit int) {
		policy := invocation.Policy{SchemaVersion: schema, CompilerVersion: compiler, DefaultsVersion: defaults, Timeout: time.Duration(timeout), ConcurrencyLimit: limit, QueueLimit: queue,
			Retry: invocation.RetryPolicy{Eligibility: eligibility, MaxAttempts: attempts, Backoff: time.Duration(backoff)}, Circuit: invocation.CircuitPolicy{FailureThreshold: circuit}}
		binding, err := policyBinding(t, policy, policyTestContract, func(context.Context, string) (string, error) {
			t.Fatal("policy validation invoked target")
			return "", nil
		})
		validRetry := (attempts == 1 && eligibility == "" && backoff == 0) || (attempts >= 2 && attempts <= invocation.MaximumRetryAttempts && eligibility == invocation.RetryReplaySafe && backoff >= 0 && timeout > 0)
		want := schema == 1 && compiler == 1 && defaults == 1 && timeout >= 0 && limit >= 1 && limit <= invocation.MaximumConcurrencyLimit && queue == 0 && circuit == 0 && validRetry
		if want {
			if err != nil || binding.Policy() != policy {
				t.Fatal("valid policy did not survive construction", err)
			}
			if _, err := invocation.NewCatalog([]invocation.Binding{binding}); err != nil {
				t.Fatal(err)
			}
		} else if !errors.Is(err, invocation.ErrInvalidPolicy) || binding.Policy() != (invocation.Policy{}) {
			t.Fatal("invalid policy was accepted")
		}
	})
}

func policyBinding(t testing.TB, policy invocation.Policy, contract capability.Contract[string, string], handler capability.Handler[string, string]) (invocation.Binding, error) {
	t.Helper()
	endpoint, err := invocation.NewEndpoint(contract, handler)
	if err != nil {
		t.Fatal(err)
	}
	build, err := invocation.NewModuleBuild("example.com/policy", "v1.0.0", "")
	if err != nil {
		t.Fatal(err)
	}
	return invocation.NewBinding(invocation.BindingOptions{Kind: invocation.BindingKindImplementation, Constructor: "example.com/policy.New",
		ModuleBuild: build, SelectionReason: invocation.SelectionReasonExplicit, ContractDigest: sha256.Sum256([]byte(contract.Identifier().String())), Policy: policy}, endpoint)
}

func policyRuntime(t testing.TB, policy invocation.Policy, handler capability.Handler[string, string]) (invocation.Handle[string, string], *invocation.Dispatcher) {
	t.Helper()
	binding, err := policyBinding(t, policy, policyTestContract, handler)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := invocation.NewCatalog([]invocation.Binding{binding})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher, err := invocation.NewDispatcher(invocation.DispatcherOptions{PolicyVersion: invocation.PolicySchemaVersion})
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.Publish(catalog); err != nil {
		t.Fatal(err)
	}
	if err := dispatcher.OpenAdmission(); err != nil {
		t.Fatal(err)
	}
	handle, err := invocation.NewHandle(dispatcher, policyTestContract, true)
	if err != nil {
		t.Fatal(err)
	}
	return handle, dispatcher
}
