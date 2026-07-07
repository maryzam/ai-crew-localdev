# ADR 0012: Provider-Declared Interception

## Status

Accepted

## Context

Workspace interception was hardcoded in the launcher: one flat scrub list mixed GitHub, SSH, git, Langfuse, OTLP, and session variables, the fail-closed git configuration was inlined in `ScrubEnv`, and the `gh` interposition was a bespoke code path. The two in-workspace shims duplicated the same load-session, mint, exec skeleton in separate binaries, and the broker composition root constructed each provider by hand. Adding a new brokered target such as AWS would have scattered more launcher wiring, scrub entries, and binaries instead of registering a declaration, and scrub behavior could not be attributed to or tested per provider.

## Decision

Each provider declares a workspace interception profile: environment variables and prefixes to scrub, fail-closed environment to force given the session, and commands to interpose. Profile types live in `internal/interception`, which depends on no other internal package. Profiles are declared in provider contract packages (`internal/providers/<name>/contract`) so shims, the launcher, and the CLI can consume them without importing provider implementations. The launcher composes the union of all profiles' scrub sets for every managed run — scrubbing is a secure default independent of which resources a session uses — and applies each profile's fail-closed environment. Command interposition symlinks are generated from the profile's declared commands.

Providers are compiled in, never loaded at runtime. Shared-object or exec'd plugins inside the governance boundary are rejected because they defeat supply-chain audit. The single registration point is `internal/providers/catalog`, which constructs the provider set for the broker and exposes the matching interception profiles; a contract test fails if a provider registers without a profile.

Every profile must satisfy the same invariant test shape: with the profile applied, every declared ambient credential is absent from the child environment, and fail-closed entries win over inherited values. The dependency check enforces the new seams: `internal/interception` imports nothing internal, provider contracts import only interception types, and runtime packages import provider contracts, never provider implementations.

## Consequences

Adding a brokered provider is now a registration: implement the port capability, declare the contract payloads and interception profile, and add both to the catalog. The launcher no longer owns provider-specific security knowledge, and per-provider fail-closed behavior is individually attributable and tested. Session-injection variables remain launcher-owned.

The two shim binaries still exist and duplicate transport skeletons; consolidating them into one multi-call binary dispatching on invocation name is separate work that this decision enables. Fail-closed environment is currently applied for all profiles on every run, which matches prior behavior; scoping it to session resources becomes possible once the launcher receives per-session resource declarations.
