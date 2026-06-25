# Product Gap Analysis

This is the source of truth for the work required to move AI Crew localdev from its current brokered-devcontainer foundation toward the north star:

> Autonomous, efficient, adaptive local dev environment: agents work inside governed project flows, security and simplicity are first priorities, quality is enforced through executable contracts, and a meta-agent layer monitors cross-project efficiency, resource use, token spend, and recurring failure patterns.

Reviewed against latest `main` at `aa868c5`.

## Current State

The repository is no longer just an auth sketch. It has a working Linux-first foundation:

- `ai-agent up` starts or finds the broker, runs host readiness checks, launches the generic devcontainer, and supports `--project` for a repository-owned devcontainer with a broker overlay, including compose-backed project devcontainers.
- The broker owns policy enforcement, GitHub App signing, token minting, same-UID peer checks, rate limits, in-memory token caching, session state, and JSONL audit events.
- `ai-agent run` creates a broker session, passes the bind secret through an inherited FD, scrubs ambient GitHub and SSH credentials, sets fail-closed git config, shims `gh`, supervises the agent process, and revokes the session on exit.
- The generic devcontainer is hardened for the supported path: reduced privileges, read-only root, broker socket mount validation, persistent agent home volume, and brokered `gh`/git tooling.
- Onboarding has improved: `ai-agent setup` can generate identities and policy, `ai-agent install` writes user systemd units, and non-interactive setup paths exist.
- Executable contracts exist for broker API shape, policy validation, session invariants, launcher auth scrubbing, memfd behavior, devcontainer readiness, persistent devcontainer home, project-devcontainer readiness, docs examples, ADR gating, semantic identifier checks, and inline-comment quality rules.

This is still not the north-star product. It is a governed credential and container substrate with useful first slices of daily workflow support.

## Near-Term Milestone

The next milestone is to start using the tool in real work while making it self-evolving and cost efficient. That refocuses the immediate product work on:

1. A low-friction path into use: one main command, one guided configuration flow, and a much shorter quick start/user manual so setup friction does not delay daily usage.
2. A real telemetry layer: Langfuse-backed run traces that can later feed a meta-agent for cross-project analysis.
3. Token and output discipline by default: visible token/cost monitoring, concise default agent guidance, quiet verification output, and project conventions that reduce noisy context before deeper automation is built.

## Priority Gaps

| Priority | Gap | Current evidence | Scope blocked |
|---|---|---|---|
| P0 | The entry path is still too heavy for daily adoption. | The user manual still leads with source build, GitHub App setup, systemd setup, `ai-agent up`, container entry, and then `ai-agent run`. `setup`, `install`, and `up` exist, but the supported path is not presented or tested as one guided flow with minimal decisions. | Daily usage, momentum |
| P0 | Telemetry is not wired into managed runs. | `contrib/langfuse/docker-compose.yml`, `make langfuse-up`, and `ai-agent up --langfuse` can start infrastructure; broker JSONL audit records auth events. There is no run trace identity, Langfuse ingestion from `ai-agent run`, verification events, token/cost events, or cross-project run history. | Self-evolution, cost visibility, meta-agent substrate |
| P0 | Token and output optimization are not defaults. | There is no default token/cost monitor, no concise project-level agent guidance such as `AGENT.md`/`CLAUDE.md`, no default policy for quiet verification output, and no token-aware conventions for summarizing logs, test failures, or repeated context. | Cost control, daily usage, adaptive efficiency |
| P0 | Agent login and state persistence are partial. | The generic devcontainer has a persistent home volume, and an integration test proves home state survives a Podman restart. There is still no supported provisioning flow for agent CLI logins/config, no executable proof that actual Claude/Codex login state survives user re-entry, and no model for separating personal agent state from governed repo credentials. | Daily use, security, simplicity |
| P1 | End-to-end readiness does not prove the full user journey. | Tests cover broker/devcontainer/project-devcontainer slices with mocked GitHub behavior, compose-backed project containers, brokered git/`gh`, ambient credential rejection, and generic home-volume persistence. They do not install from an artifact on a clean host, perform real GitHub push/PR behavior, validate actual agent login persistence, or exercise restart/re-entry as a user would. | Product confidence, release readiness |
| P1 | Project runtime support is only a first slice. | `ai-agent up --project` honors a project devcontainer, injects a read-only broker/toolchain overlay, preserves project PATH/env, and has E2E coverage for compose services, ports, brokered git/`gh`, and ambient credential rejection. It does not yet define ai-agent project manifests for secrets, caches, service policy, per-project agent defaults, approval points, or portable toolchain delivery. | Daily development, multi-project use |
| P1 | Quality contracts are repo-centric, not project-flow-centric. | `make verify`, CI, docs checks, ADR gates, invariant gates, and inline-comment gates exist. Agent runs only get an ad hoc `--verify-cmd`; there is no structured executable contract manifest per project, no failure taxonomy, and no adaptive retry plan. | Quality, autonomy |
| P1 | Meta-agent monitoring is absent. | Broker audit logs record auth events, but there is no cross-project telemetry pipeline for efficiency, token spend, resource use, repeated failures, idle loops, or coaching recommendations. | North star, efficiency |
| P1 | Governance is enforced mainly through wrappers, environment scrubbing, and PATH control. A determined or confused agent can still bypass policy by using reachable real tools, stored credentials, raw network calls, or project-provided binaries outside the supported path. | `ai-agent run` scrubs and shims the intended process tree; the generic image moves real `gh` off PATH; project mode injects wrapper tooling. There is no network egress policy, no isolated per-run home when that boundary is required, and no lower-level enforcement boundary. | Security, governed flows |
| P1 | Installation and distribution still require a source checkout and local build. | `make install` builds from source and copies binaries; project mode bind-mounts host-built binaries into containers. No release artifact, install script, checksum-verified package, or published devcontainer Feature exists. | Simplicity, clean-host onboarding, portable project mode |
| P2 | The product lacks project-aware autonomous workflow orchestration. | There is no task queue, run planner, project skill pack system, memory extraction, context budgeting, model/tool selection policy, approval flow, or local operator cockpit. | North star |
| P2 | PR automation only classifies risk tiers. | The PR tier workflow labels T1/T2/T3. It does not perform automatic review, T1 merge, post-merge revert, trace/event logging, or escalation based on observed failures. | Autonomous delivery |
| P2 | Supply-chain reproducibility is improved but incomplete. | Versions are pinned in the Dockerfile, but base images are tag-based, apt packages are mutable, downloaded `.deb` files are not checksum-verified, and global npm installs are not lockfile-backed. | Reliability, security |
| P2 | Documentation freshness still depends on manual review. | Architecture truth is now consolidated in `docs/current-north-star-architecture.md`, but README, user manual, and examples can still drift without generated checks or scenario-based docs tests. | Product truth |

## Claim Boundaries

The repository can currently claim:

- Linux-only GitHub App credential brokering for managed agent sessions.
- Host-side repo policy enforcement for broker-minted GitHub credentials.
- Fail-closed git and `gh` behavior on the supported `ai-agent run` path.
- A hardened generic devcontainer with persistent home and broker socket checks.
- First-slice project devcontainer support through a read-only broker/toolchain overlay, including compose-backed project devcontainers.
- Executable contracts around the credential broker, launcher invariants, policy schema, docs examples, devcontainer readiness, project-devcontainer readiness, and generic home-volume persistence.

The repository cannot yet claim:

- Complete prevention of intentional credential or network bypass by an agent.
- Zero-to-productive single-command onboarding from a clean host.
- Supported provisioning and re-entry for persistent agent CLI login state.
- Langfuse-backed run telemetry, token/cost accounting, or dashboards.
- Token-efficient default agent guidance and quiet verification conventions.
- Project-aware secret/cache/service/port provisioning.
- Autonomous project planning, context budgeting, model/tool choice, review, merge, or remediation.
- End-to-end observability for token spend, resource use, traces, and recurring failures.
- North-star maturity.

## North-Star Capability Map

| Capability | Current state | Next product proof |
|---|---|---|
| Governed project flows | Broker sessions, policy, wrappers, project overlay. | A project manifest that declares allowed agents, contracts, secrets, services, approval points, and run modes; enforced by `ai-agent up --project` and `ai-agent run`. |
| Security first | Strong supported-path auth controls and audit logs. | Decide the enforcement boundary for adversarial/confused agents: isolated per-run home, egress policy, real-tool removal, or explicitly documented trust limit. Then test it end to end. |
| Simple use first | `setup`, `install`, `up`, and persistent generic home storage exist. | One guided command leads through config, broker setup, devcontainer entry, agent login, and first managed run with a short quick start. |
| Executable quality contracts | Repo-local tests, gates, readiness, and `--verify-cmd`. | Project-declared contract runner with structured, quiet results, failure classes, retry guidance, and persisted run history. |
| Adaptive efficiency | Token cache and broker audit events only. | Trace every run with project, agent, model, tool calls, verification outcome, elapsed time, token/cost data, resource use, and noisy-output controls. |
| Meta-agent layer | Not implemented. | Local analyzer that reads run telemetry across projects and emits recurring-failure patterns, waste reports, and concrete workflow changes. |

## Sharp Next Steps

1. Collapse the first-use path.
   Rewrite the README quick start and user manual around one primary command and one guided configuration flow. The goal is: configure, start Langfuse, enter the workspace, verify agent login persistence, and run one governed task without reading internals.

2. Wire managed-run telemetry into Langfuse.
   Add a stable run ID and emit trace events from `ai-agent run`: project, agent, model when known, command start/stop, verification result, retry count, elapsed time, broker credential events, and links to local logs. Keep broker JSONL audit as the auth source of truth, but make Langfuse the daily operator view.

3. Add default token and cost monitoring.
   Record token/cost estimates where agent CLIs expose them, and otherwise persist best-effort usage fields with explicit `unknown` values. Surface per-run and per-project summaries so expensive loops are visible before the meta-agent exists.

4. Add token-efficient project defaults.
   Generate a concise `AGENT.md` and symlink `CLAUDE.md` to it. Default guidance should require precise, short output; thorough private reasoning; quiet test and lint commands; summarized logs; and no repeated large context unless explicitly requested.

5. Replace noisy verify/retry behavior with structured contracts.
   Keep `--verify-cmd`, but wrap it so output is quiet by default and failures are captured as structured evidence: command, exit code, short summary, relevant log tail, retry eligibility, and next prompt/context.

6. After the usage and telemetry milestone, return to the broader backlog.
   The next tranche should cover portable distribution, project manifests, stronger containment decisions, and autonomous planning/review.

## Completion Rule

A gap leaves this document only when the end-user behavior is implemented on the supported path, documented accurately, and validated by an executable test that would fail if the behavior regressed. Infrastructure alone, labels alone, or aspirational documentation do not close a product gap.
