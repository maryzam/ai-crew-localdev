# Product Gap Analysis

This is the source of truth for the work required to move AI Crew localdev from
its current brokered-devcontainer foundation toward the north star:

> Autonomous, efficient, adaptive local dev environment: agents work inside
> governed project flows, security and simplicity are first priorities, quality
> is enforced through executable contracts, and a meta-agent layer monitors
> cross-project efficiency, resource use, token spend, and recurring failure
> patterns.

## Current State

The repository is no longer just an auth sketch. It has a working Linux-first
foundation:

- `ai-agent up` starts or finds the broker, runs host readiness checks, launches
  the generic devcontainer, and supports `--project` for a repository-owned
  devcontainer with a broker overlay.
- The broker owns policy enforcement, GitHub App signing, token minting,
  same-UID peer checks, rate limits, in-memory token caching, session state, and
  JSONL audit events.
- `ai-agent run` creates a broker session, passes the bind secret through an
  inherited FD, scrubs ambient GitHub and SSH credentials, sets fail-closed git
  config, shims `gh`, supervises the agent process, and revokes the session on
  exit.
- The generic devcontainer is hardened for the supported path: reduced
  privileges, read-only root, broker socket mount validation, persistent agent
  home volume, and brokered `gh`/git tooling.
- Onboarding has improved: `ai-agent setup` can generate identities and policy,
  `ai-agent install` writes user systemd units, and non-interactive setup paths
  exist.
- Executable contracts exist for broker API shape, policy validation, session
  invariants, launcher auth scrubbing, memfd behavior, devcontainer readiness,
  project-devcontainer readiness, docs examples, ADR gating, semantic identifier
  checks, and inline-comment quality rules.

This is still not the north-star product. It is a governed credential and
container substrate with useful first slices of daily workflow support.

## Priority Gaps

| Priority | Gap | Current evidence | Scope blocked |
|---|---|---|---|
| P0 | Governance is enforced mainly through wrappers, environment scrubbing, and PATH control. A determined or confused agent can still bypass policy by using reachable real tools, stored credentials, raw network calls, or project-provided binaries outside the supported path. | `ai-agent run` scrubs and shims the intended process tree; the generic image moves real `gh` off PATH; project mode injects wrapper tooling. There is no network egress policy, no per-run clean home, and no lower-level enforcement boundary. | Security, governed flows |
| P0 | Installation and distribution still require a source checkout and local build. | `make install` builds from source and copies binaries; project mode bind-mounts host-built binaries into containers. No release artifact, install script, checksum-verified package, or published devcontainer Feature exists. | Simplicity, clean-host onboarding, portable project mode |
| P0 | Project runtime support is only a first slice. | `ai-agent up --project` honors a project devcontainer and injects the broker socket/toolchain. It does not yet define ai-agent project manifests for secrets, caches, ports, service policy, per-project agent defaults, or portable toolchain delivery. | Daily development, multi-project use |
| P0 | Agent login and state persistence are partial. | The generic devcontainer has a persistent home volume, but there is no supported provisioning flow for agent CLI logins/config, no restart/re-entry contract test proving persisted login state, and no model for separating personal agent state from governed repo credentials. | Daily use, security, simplicity |
| P0 | The product lacks project-aware autonomous workflow orchestration. | There is no task queue, run planner, project skill pack system, memory extraction, context budgeting, model/tool selection policy, approval flow, or local operator cockpit. | North star |
| P0 | Meta-agent monitoring is absent. | Broker audit logs record auth events, but there is no cross-project telemetry pipeline for efficiency, token spend, resource use, repeated failures, idle loops, or coaching recommendations. | North star, efficiency |
| P1 | End-to-end readiness does not prove the full user journey. | Tests cover broker/devcontainer/project-devcontainer slices with mocked GitHub behavior. They do not install from an artifact on a clean host, perform real GitHub push/PR behavior, validate agent login persistence, or exercise restart/re-entry as a user would. | Product confidence, release readiness |
| P1 | Observability is infrastructure, not a shipped capability. | `contrib/langfuse/docker-compose.yml` and `make langfuse-up` exist; broker JSONL audit exists. Agent instrumentation, trace identity enforcement, token accounting, dashboards, Git notes, scoring, and workflow views are not implemented. | North star, operations |
| P1 | Quality contracts are repo-centric, not project-flow-centric. | `make verify`, CI, docs checks, ADR gates, invariant gates, and inline-comment gates exist. Agent runs only get an ad hoc `--verify-cmd`; there is no structured executable contract manifest per project, no failure taxonomy, and no adaptive retry plan. | Quality, autonomy |
| P1 | PR automation only classifies risk tiers. | The PR tier workflow labels T1/T2/T3. It does not perform automatic review, T1 merge, post-merge revert, trace/event logging, or escalation based on observed failures. | Autonomous delivery |
| P2 | Supply-chain reproducibility is improved but incomplete. | Versions are pinned in the Dockerfile, but base images are tag-based, apt packages are mutable, downloaded `.deb` files are not checksum-verified, and global npm installs are not lockfile-backed. | Reliability, security |
| P2 | Documentation freshness still depends on manual review. | Architecture truth is now consolidated in `docs/current-north-star-architecture.md`, but README, user manual, and examples can still drift without generated checks or scenario-based docs tests. | Product truth |

## Claim Boundaries

The repository can currently claim:

- Linux-only GitHub App credential brokering for managed agent sessions.
- Host-side repo policy enforcement for broker-minted GitHub credentials.
- Fail-closed git and `gh` behavior on the supported `ai-agent run` path.
- A hardened generic devcontainer with persistent home and broker socket checks.
- First-slice project devcontainer support through a read-only broker/toolchain
  overlay.
- Executable contracts around the credential broker, launcher invariants,
  policy schema, docs examples, and devcontainer readiness.

The repository cannot yet claim:

- Complete prevention of intentional credential or network bypass by an agent.
- Zero-to-productive single-command onboarding from a clean host.
- A complete persistent daily development workspace.
- Project-aware secret/cache/service/port provisioning.
- Autonomous project planning, context budgeting, model/tool choice, review,
  merge, or remediation.
- End-to-end observability for token spend, resource use, traces, and recurring
  failures.
- North-star maturity.

## North-Star Capability Map

| Capability | Current state | Next product proof |
|---|---|---|
| Governed project flows | Broker sessions, policy, wrappers, project overlay. | A project manifest that declares allowed agents, contracts, secrets, services, approval points, and run modes; enforced by `ai-agent up --project` and `ai-agent run`. |
| Security first | Strong supported-path auth controls and audit logs. | Decide the enforcement boundary for adversarial/confused agents: isolated per-run home, egress policy, real-tool removal, or explicitly documented trust limit. Then test it end to end. |
| Simple use first | `setup`, `install`, and `up` exist. | Install from a signed/checksum-verified artifact, start a project, login agents, run a governed task, and re-enter after restart without reading internal docs. |
| Executable quality contracts | Repo-local tests, gates, readiness, and `--verify-cmd`. | Project-declared contract runner with structured results, failure classes, retry guidance, and persisted run history. |
| Adaptive efficiency | Token cache and broker audit events only. | Trace every run with project, agent, model, tool calls, verification outcome, elapsed time, token/cost data, and resource use. |
| Meta-agent layer | Not implemented. | Local analyzer that reads run telemetry across projects and emits recurring-failure patterns, waste reports, and concrete workflow changes. |

## Sharp Next Steps

1. Define the security boundary for governed runs.
   Decide whether the product is protecting against accidental bypass only, or
   whether agents must be prevented from intentionally escaping wrapper policy.
   If intentional bypass is in scope, prioritize per-run isolated home,
   restricted network egress, and removal or mediation of direct real-tool
   access before adding more automation.

2. Ship portable distribution.
   Add release artifacts or a pinned image with checksum verification, then
   replace project-mode host binary bind mounts with a devcontainer Feature or
   equivalent portable installation mechanism. This unlocks clean-host
   onboarding and removes the current same-host/same-architecture assumption.

3. Design the project manifest.
   Keep it separate from a repository's own devcontainer. First fields should
   cover allowed agents, executable contracts, required services, caches,
   broker-mediated secrets, ports, and run approval requirements.

4. Turn readiness into a product journey.
   Add an integration path that installs from the distribution artifact,
   performs non-interactive setup, launches a project devcontainer, proves home
   persistence across restart/re-entry, runs a managed task, and exercises a
   real or contract-faithful GitHub PR flow.

5. Promote audit logs into telemetry.
   Preserve broker JSONL audit as the auth source of truth, but add run IDs and
   connect agent sessions, verification results, token/cost data, elapsed time,
   and resource use. This is the substrate for Langfuse dashboards and the
   later meta-agent.

6. Replace ad hoc verify/retry with structured contracts.
   Introduce a contract runner that records which command failed, why it
   failed, relevant logs, retry eligibility, and the next prompt/context passed
   to the agent. Keep `--verify-cmd` as a compatibility layer over that model.

## Completion Rule

A gap leaves this document only when the end-user behavior is implemented on
the supported path, documented accurately, and validated by an executable test
that would fail if the behavior regressed. Infrastructure alone, labels alone,
or aspirational documentation do not close a product gap.
