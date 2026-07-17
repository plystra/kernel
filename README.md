# Plystra Kernel

`github.com/plystra/kernel` is the runtime half of Plystra Core. It exposes stable Go APIs used by plugins and by application source generated through the Plystra CLI.

Plystra Core is exactly:

```text
Kernel + CLI
```

The Kernel is intrinsically complete. It does not depend on the CLI at runtime and never consumes an ordinary plugin Capability or service to implement a Kernel function.

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

## Provider lifecycle

Plugins that own resources requiring explicit startup and shutdown may implement `lifecycle.Provider`. Generated assembly binds only those plugins and supplies their already-resolved deterministic order; the Kernel does not discover providers or recompute dependencies at runtime. Plugins without lifecycle work remain valid and need no lifecycle methods.

The lifecycle manager starts providers in generated order and stops them in reverse order. Failed startup performs a bounded reverse-order rollback, including the failing provider in case it acquired resources before returning. Shutdown attempts every active provider, permits retry only for providers whose stop failed, recovers panics, and exposes only safe Plugin IDs and standard context cancellation or deadline causes rather than provider error text.

## Context and configuration

The Kernel propagates ordinary Go context, cancellation, deadlines, trace correlation, and bounded opaque metadata without interpreting application identity. Generated code may carry typed AuthN-owned or AuthZ-owned values, but those remain application data.

Runtime configuration is validated and resolved through intrinsic Kernel facilities, then injected only into the owning plugin as typed data. One selected Plugin ID has one configuration object. Secret values never enter generated source, manifests, logs, traces, diagnostics, errors, or intrinsic Capability responses.

The `configuration` package is the runtime boundary used by generated typed adapters. Its per-plugin decoder rejects unknown, missing, malformed, excessively deep, and oversized values; applies declared defaults and enums; converts every supported scalar, object, and array type; and resolves only fields declared as Secrets. The matching non-resolving validation path lets build tooling check the exact same value contract without reading environment variables or files. Secret references accept only validated portable environment-variable names or clean absolute POSIX and Windows regular-file targets within a mandatory byte bound. Resolved values are immutable and defensive, while configuration objects, references, and Secrets are redacted for every formatting and structured-logging path and reject JSON, text, and YAML serialization. Errors retain only declared field names, safe failure classes, and standard cancellation or deadline causes.

## Intrinsic Capabilities

Reserved identities include:

```text
kernel.health/v1
kernel.info/v1
```

They are implemented directly by the Kernel, require no ordinary provider or `capabilities.use` entry, cannot be overridden by plugins, and remain available regardless of the selected application plugin set. HTTP exposure is still explicit, and responses redact private configuration, Secrets, sensitive paths, and unsafe build details.

## Telemetry and audit

The Kernel emits its own bounded runtime logs, metrics, traces, health state, and implementation diagnostics. It never calls `audit.write/v1`.

CLI-generated application invocation audit and explicit business audit events are separate application concerns. Removing an Audit plugin can remove its generated application behavior but cannot change Kernel telemetry or dispatch.

## Capability and plugin contracts

Provider-independent identities use the exact form `<capability-name>/v<number>`, such as `email.send/v1` or `authn.login.oidc.complete/v1`. A capability name has at least two dot-separated segments; each starts with a lower-case letter and continues with lower-case letters, digits, or hyphens. Segment count expresses logical hierarchy and never implies a fixed namespace/operation split. Callers never encode a Plugin ID or Go Module in a Capability identity.

Capability contracts, semantic errors, canonical schema digests, Plugin IDs, strict plugin manifests, and typed configuration declarations use stable Kernel packages consumed by plugins and the CLI. The Kernel's `capability/catalog` package contains only its intrinsic reserved definitions; official AuthN, AuthZ, and business contracts remain owned by their corresponding modules. Capability declarations may carry lower-kebab namespaced JSON-compatible build-time metadata. The Kernel parses and preserves that metadata as immutable, canonically ordered contract data without interpreting namespace semantics. Normalized extension metadata participates in canonical semantic equality and `SchemaDigest`; omitting or changing it makes otherwise identical provider contracts incompatible. Exact official versions are immutable, and providers of the same identity must use semantically identical contracts.

There is no separately distributed Go Plugin SDK. Plugins and generated source depend directly on the stable Kernel API.

## Assembly compatibility

Generated assembly declares the exact Kernel assembly API version it targets. The Kernel rejects incompatible assembly metadata explicitly rather than interpreting it under a different contract.

## Development

```powershell
go test ./...
go test -race ./...
go vet ./...
```
