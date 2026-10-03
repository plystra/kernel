# Plystra Kernel

`github.com/plystra/kernel` is the runtime half of Plystra Core. It exposes stable Go APIs used by plugins and by application source generated through the Plystra CLI.

Plystra Core is exactly:

```text
Kernel + CLI
```

The Kernel is intrinsically complete. It does not depend on the CLI at runtime and never consumes an ordinary plugin Capability or service to implement a Kernel function.

## License

Plystra Kernel is licensed under the [Apache License, Version 2.0](LICENSE).

Applications and plugins using the Kernel may use their own licenses, including proprietary terms, while meeting the applicable Apache-2.0 requirements. Third-party dependencies and material retain their own licenses.

## Runtime responsibilities

The Kernel owns:

- One immutable already-resolved provider registry.
- Exact typed in-process Capability lookup and dispatch.
- Provider lifecycle and graceful startup and shutdown.
- Validated typed runtime configuration injection.
- Ordinary Go `context.Context`, deadlines, cancellation, trace data, and bounded opaque metadata propagation.
- Panic recovery and safe typed error normalization.
- Runtime concurrency, timeout, queue, buffering, and memory limits.
- Intrinsic logs, metrics, traces, health, and implementation diagnostics.
- Reserved intrinsic `kernel.*` Capabilities.
- Stable plugin-facing and generated-assembly APIs.

The Kernel does not:

- Own a User, account, Person, profile, Member, Space, credential, session, token, permission, or policy model.
- Define an application-wide identity abstraction.
- Authenticate application callers or authorize business operations.
- Call AuthN, AuthZ, Audit, or another plugin before or after target dispatch.
- Depend on a plugin for logging, health, security, storage, transport, lifecycle, tracing, metrics, configuration, or another intrinsic feature.
- Scan Go Modules, choose providers, evaluate plugin build-time rules, or generate application code at runtime.

Applications without AuthN, AuthZ, a User model, or any ordinary plugin remain valid.

## Dispatch boundary

The CLI resolves providers and generates the supported invocation path before build:

```text
generated HTTP adapter, SDK adapter, or Capability client
-> generated application preparation and checks
-> Kernel exact dispatch
-> selected in-process provider
```

The Kernel receives one complete immutable registry. It distinguishes exact Capability versions, performs no runtime provider selection, and never starts with silently missing application requirements.

Go plugins share one process and are not sandboxed. Generated Capability clients are the supported application API; raw dispatch remains a low-level Kernel boundary rather than an encouraged bypass for ordinary plugin code.

`invocation.NewModuleBuild` preserves exact owning-module provenance. An
unversioned local Project may use a single-component identity such as `my-app`,
with a mandatory safe build identity. Versioned dependencies still require
standard Go Module paths and canonical versions. Short names do not alias
dependencies, and a selected constructor must remain inside its owning module.

Typed contracts declare their exact semantic error codes. Implementations return
`invocation.NewSemanticError(code, cause)`, directly or through ordinary `%w`
wrapping. `Code()` exposes the code and `Unwrap()` retains the optional cause
for local `errors.Is` and `errors.As` traversal. Formatting and structured
logging never expose the cause. The superseded structural
`capability.SemanticError` interface is not accepted.

The endpoint examines both single and joined unwrap edges, with at most 64
unwrap levels and 1,024 visited nodes. Shared subtrees are permitted within the
node budget; cycles, panicking unwrap methods, exceeded bounds, invalid or
undeclared codes, distinct semantic codes, and conflicting primary runtime
categories fail closed as `internal`. Custom `As` and `Is` methods are not
executed. Ordinary wrapping and joined errors remain recognizable. The returned
semantic carrier contains only the declared code and completion classification;
private causes, wrapper messages, and concrete implementation types are removed.

### Startup admission

`Dispatcher.Publish` installs the complete immutable catalog but does not accept
invocations. Assembly must call `Dispatcher.OpenAdmission()` only after all
selected values are ready and transports have been bound. Opening before
publication returns `ErrDispatcherNotReady` without changing state. Opening is
idempotent while accepting, but a drain permanently prevents both publication
and reopening, including when shutdown began before startup finished.

`Dispatcher.Accepting()` reports current admission, not a reservation against
concurrent shutdown. `Published()` reports only catalog installation;
`AdmissionClosed()` reports permanent shutdown closure, not startup readiness.
Available bindings reject calls before opening with safe `unavailable`,
`runtime.dispatcher_not_ready`, and `not_started`, without request preparation,
target entry, response processing, retry, or permit reservation. Such published
binding rejections record caller telemetry but no target sample. Cancellation
and deadline checks still apply. Admission is rechecked when reserving an
attempt so a drain during preparation cannot admit a target.

This is a breaking assembly behavior change. Existing manual assembly must
explicitly open admission when ready. The Kernel does not infer readiness from
catalog contents or start lifecycle values itself. Generated application-wide
startup coordination and lifecycle-manager wiring remain separate consumer
integration requirements; publication alone never permits hook calls. A bound
lifecycle order also prevents opening before every Start succeeds or while a
hook still owns target work.

### Completion classification

`invocation.CompletionOf(err)` reads the separate closed completion vocabulary:
`not_started`, `result_known`, or `result_unknown`. A nil error is a known
success. `Error.Completion()` and `SemanticError.Completion()` expose the same
classification on safe runtime and semantic outcomes. Pre-dispatch rejection
is `not_started`; an executed target cannot inherit that assertion from a
nested call. A target's ordinary returned safe error is `result_known`.
Cancellation that suppresses a dispatched target's result is `result_unknown`,
not a claim that business effects were rolled back.

Generated request validators and admission checks can construct a pre-entry
failure with `invocation.NewNotStartedError(code, detailCode)`. It validates the
same closed codes and bounded detail as `NewError`, but its completion is
`not_started`. Ordinary wrapping preserves that classification locally. If an
entered target returns such a nested rejection, the outer endpoint promotes it
to `result_known` without mutating the nested error; any accompanying
`result_unknown` still wins. Generated consumer adoption remains separate.

Use `invocation.NewResultUnknown(cause)` for an uncertain result, including
commit-acknowledgement loss. It retains an optional private cause locally and
can wrap, or be wrapped by, a semantic error. Uncertainty survives ordinary
wrapping, joins, semantic translation, and endpoint redaction without replacing
the primary runtime category or declared semantic code. The old
`ErrorResultUnknown` primary category is removed. A deadline can therefore
remain a deadline while also reporting `result_unknown`. Incomplete error-tree
traversal conservatively returns internal failure with `result_unknown`; the
public completion helper also treats unclassified or invalid errors as unknown.

### Target lifetime and drain

Dispatch runs each adapter in a tracked goroutine. Caller cancellation or the
effective deadline returns without waiting for an uncooperative target. When
this happens after entry, the caller receives `result_unknown` and the target
remains registered until its adapter actually returns, panics, or exits its
goroutine. A scheduled target
abandoned before entry cannot enter later and returns `not_started`. Late
responses and errors are discarded; a target calling `runtime.Goexit` is a
redacted internal failure rather than a zero-value success.

`Dispatcher.ActiveAttempts()` counts registered adapter executions, including
scheduled work and targets whose callers already completed.
`Dispatcher.Drain(ctx)` requires a non-nil context with a deadline, permanently
closes public admission, cancels active hook and target contexts, and waits for
actual hook-body and target termination. Invalid contexts leave admission
unchanged. An expired or
cancelled drain reports `ErrDrain` with only the standard context cause; a
fresh bounded call can resume the same drain. Concurrent callers have
independent wait deadlines. `AdmissionClosed()` reports permanent closure;
`Published()` remains an independent immutable-catalog fact, not readiness.

Drain never stops an Implementation or Resource. Its owner must leave
dependencies live on failure and invoke lifecycle cleanup only after a
successful drain. Raw handles also do not clone request graphs: generated
proxies and adapters own that isolation, and raw callers must not mutate storage
shared with a still-running target after caller completion.

`Handle.InvokeWithResponse(ctx, request, process)` lets generated proxies run
their typed response validator and copier inside the tracked attempt. A
successful target result is not released from lifetime tracking until that
processor finishes. Shutdown therefore cannot stop an Implementation or
Resource while the processor still reads its retained storage. The non-nil
processor must perform only bounded, synchronous validation and copying and
return independently owned storage; it must not retain target storage or start
background work. `Invoke` remains the raw path without response processing.

The processor is skipped on target failure, pre-entry rejection, or when the
caller has already completed before processing begins. Cancellation or timeout
during processing still completes the caller with `result_unknown`; drain
continues waiting for the processor and discards its late outcome. Invalid
response errors are safe internal failures with zero results. Valid internal
detail codes such as `contract.response_invalid` and uncertainty survive bounded
normalization, while private causes, semantic claims, panics, and `Goexit` do not
escape as successful responses or caller-authored request errors. The Kernel
does not inspect or copy application fields itself.

Generated consumer evidence is tracked separately in the philosophy roadmap;
this Kernel boundary alone does not establish lifecycle-hook dependency access
or generated consumer acceptance. Separate lifetime metrics are described under
[Telemetry and audit](#telemetry-and-audit).

### Compiled invocation policies

`BindingOptions.Policy` supplies the complete typed `invocation.Policy` value.
The immutable binding copies it and returns defensive value copies through
`Binding.Policy()`. Schema, compiler-protocol, and defaults versions must match
`PolicySchemaVersion`, `PolicyCompilerVersion`, and `PolicyDefaultsVersion`.
These are compatibility protocol versions, not Go Module release versions.
`DispatcherOptions.PolicyVersion` must match the supported schema. Unknown
versions, missing resolved defaults, unknown retry eligibility, and unsupported
stages fail before catalog publication; errors contain no rejected values.

Every policy supplies a positive `ConcurrencyLimit` and explicit
`RetryPolicy{MaxAttempts: 1}` when retries are disabled. The absence-default
contract is no policy timeout, concurrency 64, queue zero, one attempt, and a
disabled circuit. `Timeout: 0` adds no deadline. A positive timeout bounds the
entire logical call and respects an earlier caller or inherited deadline.
`MaximumPolicyDuration` is the greatest positive Go duration. Positive or
negative queue limits and any nonzero `CircuitPolicy` are rejected; queueing
and circuit execution are not implemented.

Retries require `Eligibility: invocation.RetryReplaySafe`, a positive timeout,
2 through `MaximumRetryAttempts` (16) attempts, and nonnegative backoff. The
first retry-enabled binding in a nested call chain owns all framework retries;
nested bindings retain their own timeout and concurrency but execute once.
Retry ownership is bounded Kernel-owned context state. `invocation.Current`
exposes the owner and one-based attempt number without changing application
identity or serializing metadata. A single logical invocation keeps its
invocation ID across attempts; nested calls retain ordinary ancestry.

Only pre-entry admission exhaustion and known target `unavailable` or
`resource_exhausted` outcomes may retry. Semantic errors, cancellation, timeout,
panic, internal/validation failures, and uncertain results never retry. The
next attempt waits for actual prior-target termination and interruptible
backoff under the original deadline. Late targets and response processors retain
their admission permits and cannot cause a retry after caller completion.
Shutdown interrupts backoff, closes admission, and drains retained targets.
Safe runtime and semantic errors expose `Attempts()`; zero means rejection
before the attempt loop. Exhaustion retains the final code and detail; a later
pre-entry rejection cannot erase an earlier known target result. Attempt
evidence is copied onto returned errors without mutating
shared source errors, so error pointer identity is not preserved.

Generated proxies use `Handle.InvokeWithPreparation` to start the budget before
their synchronous bounded request validator/copier. Preparation creates one
immutable snapshot and runs once. Each endpoint adapter must copy it into fresh
target-owned storage for each attempt; the Kernel never reflects over or copies
application graphs. Its tracked response processor uses the same contract as
`InvokeWithResponse`. Preparation failures never enter a target, and an expired
budget cannot admit work merely because the context timer has not fired yet.
Raw `Invoke` and `InvokeWithResponse` callers must supply their own immutable
snapshot and adapter copying when enabling retries.

This is a breaking assembly API change: the old dispatcher `DefaultTimeout`
and standalone binding `ConcurrencyLimit` inputs are removed. Generated static
timeout and replay-safe retry acceptance is recorded at `cli@f944e75` and
`cli@b91e237` in the philosophy roadmap; other policy stages remain incomplete.

### Admission accounting

`BindingOptions.Policy.ConcurrencyLimit` is mandatory and must be between 1 and
`invocation.MaximumConcurrencyLimit` (65,536), inclusive. `NewBinding` rejects
zero, negative, or over-bound values instead of silently supplying a default.
`Binding.ConcurrencyLimit()` exposes the immutable resolved value. Assembly
must supply it for every binding as part of the complete policy.

Admission is scoped to one exact Interface binding in one dispatcher, not to
a handle, constructor, or shared catalog. The current primitive has no queue:
saturation returns `ErrorResourceExhausted` (`resource_exhausted`) with
`runtime.concurrency_exhausted` and `not_started`, without starting a worker
or response processor. The runtime reserves capacity before scheduling the
target and retains it until the adapter and response processor finish, panic,
or exit. Caller cancellation, deadlines, and failed shutdown drains do not
release that capacity. A scheduled attempt abandoned before entry releases it
when its worker acknowledges abandonment. Shutdown closure takes precedence
over saturation for otherwise live calls and remains permanent after drain.

`intrinsic.NewBindings` supplies the fixed `intrinsic.ConcurrencyLimit` of 64
for each intrinsic Interface. Ordinary assembly supplies its own explicit
limits. The compiled-policy boundary above owns timeout and retry execution;
queue, circuit, and authored generated concurrency acceptance remain incomplete.

## Implementation lifecycle

Implementations that own resources requiring explicit startup and shutdown implement `lifecycle.Instance`. Constructors only assemble values and store configuration and dependencies; resource acquisition and background work belong in `Start`. Generated assembly supplies constructed lifecycle values in dependency order through `lifecycle.NewBinding` and `lifecycle.NewManager`; the Kernel does not discover Implementations or recompute dependencies. Values without lifecycle work need no lifecycle methods.

The lifecycle manager owns cleanup of every supplied constructed value from creation, including before `Start`. It starts values in generated order and stops them in reverse order. Startup cancellation, error, or panic performs bounded rollback of all constructed values, including the failing value and values whose `Start` was never entered. Rollback preserves context values but uses a fresh timeout independent of startup cancellation. `Stop` must tolerate never-started and partially started values. Cleanup attempts every pending value, skips successful stops on later calls, and leaves failed stops retryable with a fresh context. A `Stop` hook returning nil confirms cleanup even if cancellation arrived during that hook; later hooks remain pending when cancellation prevents entry. Errors and panics expose only safe constructor symbols and standard context cancellation or deadline causes, never Implementation error text.

When those values use governed Interfaces, assembly supplies
`ManagerOptions.Dispatcher` before publishing the catalog. The manager binds
its complete constructor order using `Dispatcher.BindLifecycle`; duplicate,
invalid, repeated, and post-publication lifecycle assembly is rejected. Nil is
for a manager without governed invocation. A dispatcher owns one lifecycle
order; neither API discovers constructors or dependency edges. All Interface
bindings selecting a managed constructor share its readiness. Bindings without
lifecycle work, including intrinsics, are ready after construction.

An invocation-aware manager requires deadline-bound Start and Stop contexts.
Each hook receives a private, short-lived context scope. Within that scope,
ready dependencies are callable while public admission remains closed.
Unstarted, failing, and already-stopping values return `unavailable` with
`runtime.lifecycle_not_ready` and `not_started`. Hook calls retain ordinary
invocation ancestry, policies, permits, response processing, and telemetry,
including retry during Stop. A scope cannot grant access to another dispatcher.
Returning from a hook revokes its scope and cancels outstanding work; retaining
the context or using `context.WithoutCancel` cannot extend its deadline or
lifetime. Hooks must make dependency calls synchronously within that context.

The manager drains before each pending Stop, including startup rollback. It
will not enter dependency cleanup while a hook body, target, or response
processor remains live. A failed bounded wait preserves pending values for a
fresh Stop retry and exposes safe lifecycle and drain errors. Cleanup hooks may
then call still-ready dependencies without reopening public admission; every
such hook has its own tracked lifetime. A successful Stop is retained after
cancellation only once all of its target work has terminated. Assembly still
owns application-wide coordination across dispatchers and transport readiness,
and must call OpenAdmission explicitly after complete successful startup.
Generated consumer integration is not established by these Kernel APIs.

## Context and configuration

The Kernel propagates ordinary Go context, cancellation, deadlines, trace correlation, and bounded opaque metadata without interpreting application identity. Generated code may carry typed AuthN-owned or AuthZ-owned values, but those remain application data.

`contextmetadata.WithBytes(ctx, key, value)` derives a context containing an
immutable copy of one opaque payload. `contextmetadata.Bytes(ctx, key)` returns
a fresh copy, or `nil, false, nil` for a missing key. Keys use the exact
Interface-ID lexical grammar, such as `example.request-hint/v1`, and contain
at most 128 ASCII bytes. A context lineage holds at most 32 keys, each payload
contains 1 through 262,144 bytes, and combined key plus payload storage is at
most 524,288 bytes. Invalid contexts, keys, and bounds return
`contextmetadata.ErrInvalidMetadata`; duplicate keys return
`contextmetadata.ErrMetadataConflict` without replacing a value. Insertion
never mutates the input context, and sibling contexts remain independent.

Governed calls preserve this container through ordinary context propagation.
The Kernel does not decode, log, serialize, or interpret the bytes as identity,
credentials, authority, or configuration. Each key's owner defines and checks
its payload schema. Metadata is process-local and remains outside Interface
messages and transport projections; target code must not retain the context or
payload after its attempt terminates.

Runtime configuration is validated and resolved through intrinsic Kernel facilities, then injected only into the owning plugin as typed data. One selected Plugin ID has one configuration object. Secret values never enter generated source, manifests, logs, traces, diagnostics, errors, or intrinsic Capability responses.

The `configuration` package is the runtime boundary used by generated typed adapters and bootstrap code. Its generic document loader accepts only a bounded regular file, rejects a symbolic final path, and detects file replacement or observable modification while reading without exposing paths or private values. Generic immutable object and string-map extractors let generated code select its own application sections without teaching the Kernel their names or meaning. Its per-plugin decoder rejects unknown, missing, malformed, excessively deep, and oversized values; applies declared defaults and enums; converts every supported scalar, object, and array type; and resolves only fields declared as Secrets. The matching non-resolving validation path checks the exact final value contract, while partial normalization validates only explicitly present fields and returns redacted immutable field data plus semantic digests for CLI-owned typed composition; neither path reads environment variables or files. Secret references accept only validated portable environment-variable names or clean absolute POSIX and Windows regular-file targets within a mandatory byte bound. Resolved values are immutable and defensive, while configuration objects, setting maps, references, Secrets, and partial values are redacted for every formatting and structured-logging path and reject JSON, text, and YAML serialization. Errors retain only declared field names, safe failure classes, and standard cancellation or deadline causes.

## Intrinsic Interfaces

Reserved identities include:

```text
kernel.health/v1
kernel.info/v1
```

They are implemented directly by the Kernel, require no ordinary Implementation or `interfaces.use` entry, cannot be overridden by application code, and remain available regardless of the selected application package set. HTTP exposure is still explicit, and responses redact private configuration, Secrets, sensitive paths, and unsafe build details.

The canonical authored packages are `interfaces/kernel/health/v1` and `interfaces/kernel/info/v1`. Each defines one versioned Go Interface plus its request and response types. The `intrinsic` package publishes their deterministic reserved inventory and constructs both executable Kernel bindings in canonical ID order. Assembly supplies only validated Kernel Go Module provenance; the bindings use intrinsic selection, carry no application Implementation identity, and can form a complete catalog with no ordinary bindings. `kernel.health/v1` returns only `healthy`. `kernel.info/v1` returns assembly API `v1`, `github.com/plystra/kernel`, and the canonical Kernel module version, or the fixed `devel` marker for an unversioned build; build identities and other private build details are not returned.

## Telemetry and audit

The Kernel owns intrinsic runtime telemetry and never calls `audit.write/v1`.
The implemented invocation telemetry is two OpenTelemetry duration histograms
under the `github.com/plystra/kernel/invocation` instrumentation scope:

| Instrument | Sample boundary |
| --- | --- |
| `plystra.invocation.caller.duration` | One sample per logical call after resolving an available published binding, through caller completion, including preparation, attempts, and backoff |
| `plystra.invocation.target.duration` | One sample per entered target attempt, from entry through adapter and response-processor termination |

Both use seconds and fixed bucket boundaries of 0.001, 0.005, 0.01, 0.05, 0.1,
0.5, 1, 5, 10, 30, and 60 seconds unless the application's SDK view overrides
aggregation. Histogram counts therefore distinguish logical calls from actual
target executions. Pre-entry rejection contributes no target sample. A late
success, failure, panic, or goroutine exit cannot rewrite the caller's recorded
outcome, release a permit early, or create another call sample.

Labels are limited to `plystra.interface.id`,
`plystra.implementation.constructor` (empty for intrinsic bindings),
`plystra.invocation.outcome` (`success`, `runtime_error`, or `semantic_error`),
`plystra.error.code` (empty on success, a closed runtime code, or a declared
semantic code), and `plystra.invocation.completion`. Target samples also carry
the boolean `plystra.invocation.target.late`, which reports whether the caller
had abandoned that attempt when its termination was observed. They include no
request, response, arbitrary error text or detail code, opaque context metadata,
request/trace/invocation ID, source path, module build identity, or configuration.
Metric recording receives a background context, so it does not propagate caller
data or trace exemplars. Dormant bindings emit nothing.

`DispatcherOptions.MeterProvider` optionally supplies the application's
OpenTelemetry provider; nil uses the standard global delegating provider. With
no installed SDK, instruments are no-ops and the Kernel starts no exporter,
network connection, background reader, or telemetry queue. The owner configures
bounded SDK aggregation/export and shuts down its provider after invocation
drain. The Kernel neither takes ownership of that provider nor requires an
application Implementation for metrics. Instrument construction errors fail
dispatcher creation with redacted `ErrInvalidTelemetry`; recording panics do
not replace invocation results or prevent attempt cleanup. Custom providers
must obey the synchronous, concurrency-safe OpenTelemetry API contract.

This producer boundary does not establish generated-Project telemetry
acceptance, governed spans, broader intrinsic logs, or telemetry export setup.

CLI-generated application invocation audit and explicit business audit events are separate application concerns. Removing an Audit plugin can remove its generated application behavior but cannot change Kernel telemetry or dispatch.

## Capability and plugin contracts

Provider-independent identities use the exact form `<capability-name>/v<number>`, such as `email.send/v1` or `authn.login.oidc.complete/v1`. A capability name has at least two dot-separated segments; each starts with a lower-case letter and continues with lower-case letters, digits, or hyphens. Segment count expresses logical hierarchy and never implies a fixed namespace/operation split. Callers never encode a Plugin ID or Go Module in a Capability identity.

Capability contracts, typed operation semantics, closed field constraints, semantic errors, canonical schema digests, Plugin IDs, strict plugin manifests, and typed configuration declarations use stable Kernel packages consumed by plugins and the CLI. Every canonical Capability declaration carries one closed `semantics` object describing its kind, effects, idempotency, retry safety, cancellation, completion, ordering, and request/response data classification. String, integer, number, and array fields may carry only their type-specific bounded constraint vocabulary; the Kernel validates and exposes immutable normalized constraints, and includes them in exact contract equality and `SchemaDigest`. The Kernel validates the operation-specific consistency rules and exact request-field references, then exposes the immutable normalized values to the CLI; Providers cannot override them. The Kernel's `capability/catalog` package contains only its intrinsic reserved definitions; official AuthN, AuthZ, and business contracts remain owned by their corresponding modules. Capability declarations may carry lower-kebab namespaced JSON-compatible build-time metadata. The Kernel parses and preserves that metadata as immutable, canonically ordered contract data without interpreting namespace semantics. Normalized constraints, extension metadata, and typed semantics participate in canonical semantic equality and `SchemaDigest`; omitting or changing any of them makes otherwise identical provider contracts incompatible. Exact official versions are immutable, and providers of the same identity must use semantically identical contracts.

There is no separately distributed Go Plugin SDK. Plugins and generated source depend directly on the stable Kernel API.

## Assembly compatibility

Generated assembly declares the exact Kernel assembly API version it targets. The Kernel rejects incompatible assembly metadata explicitly rather than interpreting it under a different contract.

## Development

Run validation from the Kernel module root. Set `GOWORK=off` once for the
PowerShell session so every command proves that the module is independent of a
workspace file:

```powershell
$env:GOWORK = "off"

go test ./... -count=1
go test -race ./... -count=1
go vet ./...
go mod verify
```

Go runs one fuzz target per command. The following PowerShell discovers every
committed `Fuzz...` function, derives its package, and runs each target with a
10-second local bound:

```powershell
$fuzzTargets = Get-ChildItem -Recurse -File -Filter "*_test.go" |
    Select-String -Pattern '^func (Fuzz[A-Za-z0-9_]+)\(f \*testing\.F\)' |
    ForEach-Object {
        $relativePackage = [IO.Path]::GetRelativePath(
            (Get-Location).Path,
            (Split-Path -Parent $_.Path)
        ).Replace('\', '/')

        [pscustomobject]@{
            Package = if ($relativePackage -eq '.') { '.' } else { "./$relativePackage" }
            Target  = $_.Matches[0].Groups[1].Value
        }
    } |
    Sort-Object Package, Target -Unique

if (-not $fuzzTargets) {
    throw "No fuzz targets found."
}

foreach ($fuzz in $fuzzTargets) {
    go test $fuzz.Package -run '^$' -fuzz ('^{0}$' -f $fuzz.Target) -fuzztime 10s
    if ($LASTEXITCODE -ne 0) {
        throw "Fuzz target failed: $($fuzz.Package) $($fuzz.Target)"
    }
}
```

The 10-second duration is a reproducible bounded local validation choice, not
a universal sufficiency threshold. Increase `-fuzztime` for longer campaigns.

Run the committed intrinsic Kernel and error-boundary benchmarks separately so their
allocation and timing results remain attributable:

```powershell
go test ./invocation -run '^$' -bench '^BenchmarkCapabilityLookup$' -benchmem -count=5
go test ./invocation -run '^$' -bench '^BenchmarkRegistryConcurrentRead$' -benchmem -count=5
go test ./invocation -run '^$' -bench '^BenchmarkKernelCanonicalDispatch$' -benchmem -count=5
go test ./invocation -run '^$' -bench '^BenchmarkInvocationErrorBoundary$' -benchmem -count=5
go test ./invocation -run '^$' -bench '^BenchmarkCancelledTargetDrain$' -benchmem -count=5
go test ./invocation -run '^$' -bench '^BenchmarkKernelResponseProcessing$' -benchmem -count=5
go test ./invocation -run '^$' -bench '^BenchmarkAdmissionConcurrent$' -benchmem -count=5
go test ./invocation -run '^$' -bench '^BenchmarkCompiledPolicy$' -benchmem -count=5
go test ./invocation -run '^$' -bench '^BenchmarkInvocationLifetimeMetrics$' -benchmem -count=5
```

Benchmark results depend on the machine and Go toolchain. Record that context
with local evidence and compare applicable results with the binding Kernel
runtime performance requirements; do not treat one machine's numbers as
universal expectations. The cancelled-target drain benchmark includes fresh
dispatcher and catalog construction because admission closure is permanent.
