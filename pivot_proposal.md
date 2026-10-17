# AI Crew localdev pivot

Date: 2026-10-18

Status: proposed north star and decision framework

## Decision

AI Crew localdev becomes a secure local substrate for native coding agents, not another coding-agent harness.

Its core outcome is:

> Start Claude Code or Codex in one command inside an isolated rootless Podman environment, let the agent work freely on the project, prevent durable or excessive access to external systems, and persist enough correlated evidence to understand and improve the work later.

Claude Code and Codex run unmodified and retain their native user experience, reasoning, sessions, subagents, skills, hooks, reviews, and context management. AI Crew localdev owns only the boundary around them: container lifecycle, external capabilities, durable evidence, and the minimum shared configuration needed to connect those parts.

## Change in intent

The previous design treated AI Crew localdev as a governed development workflow with agent identities, custom plans, managed runs, private workspaces, an apply step, budgets, and provider-specific orchestration.

That is no longer the product.

Native agent CLIs now provide the agent loop and extension mechanisms well enough. Recreating those features adds latency, maintenance, and cognitive load without strengthening the important boundaries. The new design composes with native CLIs through their supported hooks, skills, configuration, structured output, and OpenTelemetry interfaces.

The product is now deliberately narrower:

- Rootless Podman protects the host and limits the project-visible filesystem.
- A host capability broker protects GitHub, AWS, and future external systems.
- Wrappers give agents safe, predictable command semantics while provider-side policy remains authoritative.
- A local evidence pipeline correlates native telemetry, logs, external capabilities, and repository state.
- Later workflow intelligence is built from this evidence rather than embedded in the initial runtime.

## Decision priorities

When goals conflict, use this order:

1. Enforceable security and protection from irreversible external actions.
2. Simple, reliable operation with a small understandable surface.
3. Durable traceability with no silent loss of security or governance evidence.
4. Fast autonomous progress toward useful outcomes.
5. Efficient token, cache, model, and infrastructure use.
6. Extensibility and compatibility with additional agents or legacy workflows.

Backward compatibility is not a reason to preserve the current architecture. Generality is added only after a second concrete use case proves it is needed.

Every operational trade-off must have a measurable budget, emitted evidence, and deterministic failure policy. Security and governance paths fail closed; optional observability destinations may fail without stopping local work because canonical evidence remains local.

## Ownership boundary

| Concern | Native Claude Code and Codex | AI Crew localdev |
|---|---|---|
| Reasoning, coding, planning, subagents, context, review | Own | Do not duplicate |
| Interactive and non-interactive agent UX | Own | Launch unchanged |
| Skills and project instructions | Load and execute | Curate and synchronize later |
| Tool lifecycle integration | Expose hooks and structured events | Install thin adapters |
| Project execution isolation | Secondary native safeguards | Rootless Podman is the default boundary |
| GitHub and AWS authority | Consume short-lived capabilities | Hold durable authority and enforce maximum scope |
| External writes | Request intent | Classify, record, approve, and execute on the host |
| Telemetry production | Emit native OTel and structured events | Correlate, persist, normalize, and fan out |
| Cross-provider improvement | No shared ownership | Build later from measured local evidence |

Native hooks improve the interaction and can rewrite or annotate tool calls, but they are not the security boundary. An agent with broad access inside its container can bypass a wrapper. External security therefore comes from host custody, provider-enforced permissions, short lifetimes, repository protection, and host-mediated write actions.

## Immediate v0

The first usable version contains only three foundations.

### 1. Easy rootless Podman environment

The initial operator surface is:

```text
ai init
ai up [claude|codex|shell]
ai logs [run-id]
ai doctor
```

`ai init` creates a small project profile and an optional project Containerfile. `ai up` builds or reuses the project image, starts the container, assigns a run ID, attaches the broker and telemetry endpoints, and starts the selected native CLI.

The container mounts only the selected project, explicitly declared caches, isolated Claude/Codex login state, and the broker socket. It never mounts the operator's home, SSH agent, AWS configuration, GitHub configuration, or sibling repositories. Dependency and CLI-login volumes survive container replacement; the container itself is disposable.

The agent has high trust inside the container and can install dependencies, run arbitrary project tools, and change project files without routine approval prompts. That trust does not extend to the host or external systems.

### 2. GitHub and AWS capability boundary

Long-lived GitHub App keys, AWS refresh credentials, private keys, and equivalent authority remain on the host.

The container receives a repository-scoped read-only GitHub token and a short-lived AWS STS session for one project-approved role. AWS sessions use the run ID as source identity, deny role chaining, and are read-only by default. Provider-side permissions are the final maximum authority even if a wrapper is bypassed.

GitHub writes never receive a write token inside the container. The v0 host gateway supports only normal append-only pushes, draft pull-request creation or update, and comments on the bound pull request. It resolves the repository and branch itself, rejects force pushes, branch deletion, unexpected refspecs, and non-descendant history, records authorization durably, then performs the action with a host-only short-lived credential.

Published history is append-only. Review corrections are follow-up or revert commits; amend, rebase of published work, hard-reset removal, force push, and administrative bypass are outside the supported path.

The `git`, `gh`, and `aws` wrappers classify intent and select one deterministic result:

- Allow a permitted read.
- Replace the request with a semantically equivalent restricted invocation.
- Run a project-declared safe alternative.
- Submit a supported host action.
- Request human approval when that capability exists later.
- Refuse when no safe alternative exists.

Safe alternatives are explicit argv arrays in host-controlled policy, never shell fragments or guesses generated from the attempted command. The wrapper always states whether the original action occurred. Unexpected privilege attempts and every redirect are recorded with the original action class, selected alternative, goal or run context, and outcome.

### 3. Durable telemetry and logs

Every launch receives a stable run ID. The evidence pipeline captures:

- Native Claude/Codex OpenTelemetry signals.
- Native hook and structured CLI events.
- Agent stdout and stderr where the interface exposes them safely.
- Container lifecycle and image/configuration identity.
- Broker credential issuance, policy decisions, redirects, and host actions.
- Repository, branch, commit, agent, model, session, token, cache, timing, and result context.

Raw append-only event files are the complete replayable record. SQLite is the searchable run, audit, and correlation index. An ingestion acknowledgement is not successful until required local evidence is durable.

The local OpenTelemetry path normalizes the minimum common session, turn, model, and tool structure and sends it through an OTel Collector to both Langfuse and Phoenix. Those systems are rebuildable views used side by side; neither is the source of truth. Raw transcripts and prompt or response content are opt-in, redacted where configured, and retention-bounded.

The agent CLIs already expose the extension points required for this boundary: Codex provides hooks and OTel configuration, while Claude Code provides hooks and detailed OTel monitoring. Langfuse and Phoenix both accept OTLP traces. These are integration contracts, not functionality for this repository to recreate.

## v0 exclusions

The following are intentionally not part of the first foundation:

- Goal or specification workflow.
- Cross-model or adversarial review orchestration.
- Scheduler, rate-limit recovery, or unattended continuation.
- Mobile approvals or Telegram integration.
- Meta-optimizer, retrospectives, findings experiments, or automatic skill updates.
- AWS write operations.
- Hard token or cost budgets.
- Per-agent identity.
- Gemini support.
- Private checkout and apply workflow.
- Worktree policy or other prescribed development methodology.
- Devcontainer overlay generation, Docker compatibility, or general environment orchestration.
- Generic provider, plugin, or policy frameworks without a proven second implementation.

The evidence schema reserves optional goal, slice, skill, rule, decision, and review identifiers so later layers do not require a telemetry redesign.

## What may be built next

New layers are considered only after v0 is used for real work and its boundaries are proven.

1. A small shared skills and configuration library synchronized into native Claude and Codex extension points.
2. Lightweight iterative goals based on a short JTBD statement, explicit constraints, the next testable slice, and versioned goal pulses rather than an exhaustive initial specification.
3. Risk-based independent review, shared limit recovery, and concise remote human decisions.
4. A meta-optimizer that combines deterministic aggregation and anomaly detection with selective AI diagnosis and human-approved skill, rule, and routing experiments.

These layers consume the foundation; they do not expand the authority of the container or replace native agent loops.

## Implementation strategy

The v0 implementation starts greenfield inside this repository. The current implementation is frozen as a source of tested primitives and security lessons, not refactored into the new shape.

The new code must not import legacy orchestration, workspace, identity, launcher, devcontainer, or Langfuse-provider packages. Small proven pieces may be extracted after focused review: GitHub signing and permission-subset logic, secure atomic persistence, audit-before-action behavior, and useful native telemetry parsing.

The intended v0 is approximately 3,000 to 5,000 production Go lines across fewer than ten product packages, plus Containerfile and OTel Collector configuration. This is a design constraint, not a quota: exceeding it requires a concrete security or operability justification.

Once v0 passes its end-to-end proofs, legacy code and obsolete active documentation are removed in normal append-only commits. Repository history remains the archive; no history is rewritten.

## Proof of alignment

The v0 foundation is complete only when automated checks demonstrate all of the following:

- A new project can start Claude Code or Codex through one documented path.
- The container cannot access host GitHub or AWS durable credentials, the operator home, or unrelated repositories.
- GitHub reads work, direct writes fail, and the host gateway performs only recorded append-only writes.
- AWS access uses the configured role, short lifetime, run source identity, and provider-enforced maximum permissions.
- Wrapper bypass cannot increase provider authority, and unexpected privileged intent produces durable evidence.
- Required audit evidence is durable before credentials or host actions are released.
- Agent, container, broker, repository, token, cache, timing, and policy evidence is correlated under the same run ID.
- A run remains inspectable locally when Langfuse, Phoenix, or the network is unavailable.
- The same normalized run can be inspected in both Langfuse and Phoenix.
- Container replacement preserves only the explicitly intended CLI login and dependency caches.
- Claude Code and Codex remain native, upgradeable components rather than forked or reimplemented runtimes.

Any proposed feature that does not strengthen these proofs or build directly on their evidence is outside the current product focus.
