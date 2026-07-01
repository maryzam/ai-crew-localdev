# ADR 0006: Explicit Provider Boundaries

## Status

Accepted

## Context

Broker transport types, provider capabilities, provider implementations, wrapper authentication, and CLI composition previously shared the broker package or imported its concrete integrations. Those dependencies allowed provider details to leak into clients and made boundary violations review conventions instead of deterministic failures.

## Decision

The socket contract lives in `internal/brokerapi` without implementation dependencies. Provider capabilities live in `internal/brokerport` and may depend on the transport contract. Broker core owns authorization, sessions, cache, rate limiting, and audit decisions without importing provider implementations. Concrete providers own external clients, signing, configuration, resource grammar, and payload contracts under `internal/providers`. Wrappers use the shared `internal/sessionauth` loader and payload-only provider contracts. Executable roots construct concrete provider services and inject them into CLI adapters. `scripts/check-dependencies.sh` rejects forbidden imports in local verification and CI.

Managed-session authentication accepts only an exactly 32-byte secret from a memfd carrying write, grow, shrink, and further-sealing protections.

## Consequences

Broker clients can evolve against a stable transport contract, provider implementations cannot call broker concrete integrations, and wrapper dependencies remain narrow. Adding a provider requires a provider implementation, an optional payload contract, composition at an executable root, and dependency-check coverage. CLI command construction still uses a mutable configured service value until Phase 3 replaces package-global command state with constructed dependencies.
