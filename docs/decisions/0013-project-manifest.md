# ADR 0013: Project Manifest

## Status

Accepted

## Context

Project repositories supply runtime inputs (a devcontainer, a working tree, a git remote) but declare no workflow expectations to ai-agent. Quality verification is an ad hoc `--verify-cmd` flag with a fixed retry count, and agent selection plus model defaults live only in host-side configuration. The north-star architecture names a project manifest as the source of workflow truth, declaring what agents may work on a project and which executable contracts define done. Without a first-class declaration, every project-aware behavior would grow its own flag or host config knob.

## Decision

Introduce `internal/configmodel/manifest`, an in-repo project declaration discovered at `.ai-agent/manifest.json` under the repository root. The only supported schema is `ai-agent-manifest/v2`. It declares quality contracts, allowed agents, per-agent model defaults, and the supported project operating model: brokered resources, caches, services, ports, run modes, and token resource budgets. Parsing is strict: undeclared fields are rejected, not ignored. Validation follows the policy pattern — `ValidateResult` with errors and warnings — and the loader enforces a one-megabyte size cap.

The manifest is project-owned repository content, so it is read with normal file permissions rather than the owner-only, journaled store used for host governance config. It declares intent that host-side components consume; it never enforces anything itself and never carries credentials or durable secrets. Enforcement remains where it is today: the broker for credentials and the launcher plus quality service for run behavior.

## Consequences

Consumers now enforce the supported manifest declarations. `ai-agent run` executes manifest-declared contracts in place of ad hoc verification, enforces the agent identity allowlist with configured-tool binding, records model defaults as run attribution, requests declared broker resources after broker-authoritative preflight, applies manifest token budgets, and rejects disallowed run modes before broker session creation. `ai-agent up --project` validates the same manifest, rejects disallowed project-devcontainer mode and cache targets that overlap reserved ai-agent paths, injects declared cache volumes, forwards declared ports, includes declared compose services, and projects declared observability resources into the container environment without exposing durable provider credentials.

Schema growth is deliberate: each new domain requires a schema version bump, validation, and a consuming enforcement path in the same release, keeping the repo rule that documentation and declaration are not enforcement. Rejecting unknown fields means older binaries fail loudly on newer manifests instead of partially honoring them.
