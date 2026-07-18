# ADR 0013: Project Manifest

## Status

Accepted

## Context

Project repositories supply runtime inputs (a devcontainer, a working tree, a git remote) but declare no workflow expectations to ai-agent. Quality verification is an ad hoc `--verify-cmd` flag with a fixed retry count, and agent selection plus model defaults live only in host-side configuration. The north-star architecture names a project manifest as the source of workflow truth, declaring what agents may work on a project and which executable contracts define done. Without a first-class declaration, every project-aware behavior would grow its own flag or host config knob.

## Decision

Introduce `internal/configmodel/manifest`, an in-repo project declaration discovered at `.ai-agent/manifest.json` under the repository root. Schema v1 (`ai-agent-manifest/v1`) declares quality contracts (named checks with a command and a `retry` policy of `agent` or `never`) and agents (an allowlist plus per-agent model defaults). Schema v2 (`ai-agent-manifest/v2`) keeps that surface and adds the supported project operating model: brokered resources, brokered secret resource bindings, caches, services, ports, approvals, run modes, and token resource budgets. Parsing is strict: undeclared fields are rejected, not ignored, and v1 manifests cannot use v2 fields. Validation follows the policy pattern — `ValidateResult` with errors and warnings — and the loader enforces a one-megabyte size cap.

The manifest is project-owned repository content, so it is read with normal file permissions rather than the owner-only, journaled store used for host governance config. It declares intent that host-side components consume; it never enforces anything itself and never carries credentials or durable secrets. Enforcement remains where it is today: the broker for credentials and the launcher plus quality service for run behavior.

## Consequences

Consumers now enforce the supported manifest declarations. `ai-agent run` executes manifest-declared contracts in place of ad hoc verification, enforces the agent identity allowlist with configured-tool binding, records model defaults as run attribution, requests declared broker resources and secret-backed resources, applies manifest token budgets, rejects disallowed run modes, and fails closed for unsupported approval points before broker session creation. `ai-agent up --project` validates the same manifest, rejects disallowed project-devcontainer mode and reserved ai-agent cache targets, injects declared cache volumes, forwards declared ports, includes declared compose services, and projects declared observability resources into the container environment without exposing durable provider credentials.

Schema growth is deliberate: each new domain requires a schema version bump, validation, and a consuming enforcement path in the same release, keeping the repo rule that documentation and declaration are not enforcement. Rejecting unknown fields means older binaries fail loudly on newer manifests instead of partially honoring them.
