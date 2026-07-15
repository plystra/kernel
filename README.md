# Plystra Kernel

`github.com/plystra/kernel` is the runtime half of Plystra Core. It exposes the stable Go APIs used by plugins and by source generated through the Plystra CLI.

The module is intentionally independent from the CLI and from official plugin modules. Authentication and authorization implementations belong in `github.com/plystra/authn` and `github.com/plystra/authz`, not in the Kernel.

Provider-independent capability identities are parsed by `capability.ParseIdentifier`. Exact major versions are mandatory and canonical, for example `email.send/v1`; callers never encode a provider or Go Module in that identity.

Concrete implementation identities are parsed separately by `plugin.ParseID`, for example `acme.email.smtp`. A Plugin ID is never a capability identity and carries no independent version; the containing Go Module supplies distribution versioning.

Plugin configuration declarations use the strict DSL parsed by `plugin/manifest.ParseConfig`. Supported types are `string`, `integer`, `number`, `boolean`, `duration`, `url`, `secret`, `object`, and `array`; secret fields cannot contain defaults, and all generated defaults and enums have deterministic JSON forms.

The current `plugin.yaml` envelope is parsed by `plugin/manifest.ParsePlugin`. It requires a concrete `id` and accepts optional `provides`, `requires`, and `config` fields. Unknown fields, duplicate keys or capabilities, YAML references, multiple documents, and non-canonical identities are rejected.

Capability contracts use the strict `plugin/manifest.ParseCapability` parser. A `capability.yaml` contains an exact `id`, optional description, request and response field mappings, and semantic error codes. Contract fields support the JSON-oriented `string`, `integer`, `number`, `boolean`, `object`, and typed `array` forms used by Go and JavaScript generation.

## Assembly compatibility

Generated assembly source declares the assembly API version it targets. The Kernel validates that version through `assembly.RequireVersion` before accepting generated assembly metadata. The current contract is `assembly.V1`; incompatible versions fail explicitly instead of being interpreted by a different runtime contract.

## Development

```powershell
go test ./...
go test -race ./...
go vet ./...
```
