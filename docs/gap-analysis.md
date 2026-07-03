# Product Gap Analysis

This is the source of truth for the work required to move AI Crew localdev from its current brokered-devcontainer foundation toward the north star:

> Autonomous, efficient, adaptive local dev environment: agents work inside governed project flows, security and simplicity are first priorities, quality is enforced through executable contracts, and a meta-agent layer monitors cross-project efficiency, resource use, token spend, and recurring failure patterns.

Reviewed against `main` at `d551d7a` after broker-owned Langfuse egress (PR 74) merged.

## Current State

The repository is no longer just an auth sketch. It has a working Linux-first foundation:

- `ai-agent up` is the primary entrypoint after installation: it guides missing default configuration, starts or finds the broker, runs host readiness checks, launches the generic devcontainer, and supports `--project` for a repository-owned devcontainer with a broker overlay, including compose-backed project devcontainers.
- The broker owns policy enforcement, provider registration, GitHub App signing and token minting, Langfuse telemetry egress, same-UID peer checks, rate limits, in-memory token caching, session state, and JSONL audit events. Durable GitHub and Langfuse secrets remain inside the broker process.
- `ai-agent run` creates a broker session, assigns a stable run ID, writes inspectable managed-run telemetry, optionally relays a sanitized OTLP projection through the authenticated broker session, passes the bind secret through an inherited FD, scrubs ambient GitHub, SSH, OpenTelemetry, and Langfuse credentials, sets fail-closed git config, shims `gh`, supervises the agent process, runs verification when requested, and revokes the session on exit.
- The generic devcontainer is hardened for the supported path: reduced privileges, read-only root, broker socket mount validation, persistent agent home volume, and brokered `gh`/git tooling. `ai-agent up` explains the Claude/Codex first-login and re-entry flow, `ai-agent auth status` reports each agent's login state with remediation, and real Codex login plus both offline Claude persisted-login paths (an `apiKeyHelper` and a persisted OAuth credentials file) are exercised across container replacement.
- Onboarding has improved: `ai-agent setup` can generate identities and policy, `ai-agent install` writes user systemd units, and non-interactive setup paths exist.
- Executable contracts exist for broker API shape, policy validation, provider capability registration, broker-owned telemetry egress, session invariants, launcher auth scrubbing, memfd behavior, package dependency boundaries, bounded quality evidence, devcontainer readiness, persistent Codex login state, persistent Claude login state across container replacement, the `ai-agent auth status` login probe, project-devcontainer readiness, docs examples, ADR gating, semantic identifier checks, and a self-documenting source policy.

This is still not the north-star product. It is a governed credential and container substrate with useful first slices of daily workflow support.

## Near-Term Milestone

The next milestone is to start using the tool in real work while making it self-evolving and cost efficient. That refocuses the immediate product work on:

1. Continued reduction of first-use friction beyond the current guided `ai-agent up` path, especially portable installation, agent login provisioning, and clean-host verification.
2. Build on usage-correlated managed-run history and brokered remote export with coverage reports, dashboards, and later meta-agent analysis.
3. Token and output discipline by default: visible token/cost monitoring, concise default agent guidance, quiet verification output, and project conventions that reduce noisy context before deeper automation is built.

## Priority Gaps

| Priority | Gap | Current evidence | Scope blocked |
|---|---|---|---|
| P0 | Adaptive optimization is incomplete. | Managed Claude and Codex runs capture provider-reported request usage through native OpenTelemetry. Run history and Langfuse share normalized fields with source, scope, precision, and confidence. Remote delivery is sanitized, session-authorized, broker-owned, and bounded by payload, rate, and timeout limits. Verification output, retry count, command output, and evidence retention also have hard limits. Coverage is not yet validated across every supported login and provider mode, and no meta-agent acts on the data. | Adaptive efficiency |
| P1 | End-to-end readiness does not prove the full user journey. | Tests cover broker/devcontainer/project-devcontainer slices with mocked GitHub behavior, compose-backed project containers, brokered git/`gh`, ambient credential rejection, and real Codex login reuse. They do not install from an artifact on a clean host, perform real GitHub push/PR behavior, perform live Claude OAuth, or exercise restart/re-entry through the full user-facing CLI journey. | Product confidence, release readiness |
| P1 | Project runtime support is only a first slice. | `ai-agent up --project` honors a project devcontainer, injects a read-only broker/toolchain overlay, preserves project PATH/env, and has E2E coverage for compose services, ports, brokered git/`gh`, and ambient credential rejection. It does not yet define ai-agent project manifests for secrets, caches, service policy, per-project agent defaults, approval points, or portable toolchain delivery. | Daily development, multi-project use |
| P1 | Quality contracts are repo-centric, not project-flow-centric. | `make verify`, CI, docs checks, ADR gates, invariant gates, and source-comment gates exist. `ai-agent check` runs arbitrary commands with bounded output, classified exit status, and retained local failure evidence; managed runs still receive only an ad hoc `--verify-cmd` with a fixed retry count. There is no structured executable contract manifest per project, failure taxonomy, or adaptive retry plan. | Quality, autonomy |
| P1 | Meta-agent monitoring is absent. | Managed runs provide normalized outcomes, retries, duration, model signals, and provider-reported usage to local history and Langfuse. No cross-project analyzer turns that evidence into waste reports or proposed defaults. | North star, efficiency |
| P1 | Governance is enforced mainly through the broker, wrappers, environment scrubbing, and PATH control. A determined or confused agent can still bypass supported-path policy by using reachable real tools, stored credentials, raw network calls, or project-provided binaries. | Durable GitHub and Langfuse secrets stay in the broker, telemetry egress is independently authorized and validated, `ai-agent run` scrubs and shims the intended process tree, the generic image moves real `gh` off PATH, and project mode injects wrapper tooling. There is no general network egress policy, no isolated per-run home when that boundary is required, and no lower-level runtime enforcement boundary. | Security, governed flows |
| P1 | Installation and distribution still require a source checkout and local build. | `make install` builds from source and copies binaries; project mode bind-mounts host-built binaries into containers. No release artifact, install script, checksum-verified package, or published devcontainer Feature exists. | Simplicity, clean-host onboarding, portable project mode |
| P2 | The product lacks project-aware autonomous workflow orchestration. | There is no task queue, run planner, project skill pack system, memory extraction, context budgeting, model/tool selection policy, approval flow, or local operator cockpit. | North star |
| P2 | PR automation only classifies risk tiers. | The PR tier workflow labels T1/T2/T3. It does not perform automatic review, T1 merge, post-merge revert, trace/event logging, or escalation based on observed failures. | Autonomous delivery |
| P2 | Supply-chain reproducibility is improved but incomplete. | Versions are pinned in the Dockerfile, but base images are tag-based, apt packages are mutable, downloaded `.deb` files are not checksum-verified, and global npm installs are not lockfile-backed. | Reliability, security |
| P2 | Documentation freshness still depends on manual review. | Architecture truth is now consolidated in `docs/current-north-star-architecture.md`, but README, user manual, and examples can still drift without generated checks or scenario-based docs tests. | Product truth |

## Claim Boundaries

The repository can currently claim:

- Linux-only GitHub App credential brokering for managed agent sessions.
- A guided `ai-agent up` first-use path after installation and GitHub App creation, including inline config generation, broker startup, readiness checks, devcontainer entry, and documented first managed run.
- Host-side repo policy enforcement for broker-minted GitHub credentials.
- Fail-closed git and `gh` behavior on the supported `ai-agent run` path.
- A hardened generic devcontainer with persistent home and broker socket checks.
- Documented Claude/Codex first-login and re-entry in the generic devcontainer, with real Codex login reuse and both offline Claude persisted-login paths (`apiKeyHelper` and a persisted OAuth credentials file) reused across container replacement, an `ai-agent auth status` login probe with remediation, and GitHub repo credentials kept on the brokered path.
- First-slice project devcontainer support through a read-only broker/toolchain overlay, including compose-backed project devcontainers.
- Inspectable managed-run history with stable run and task IDs, versioned metadata, model attribution, verification attempts, optional brokered OTLP export, and broker audit correlation.
- Native Claude and Codex usage capture with request-level provider attribution.
- Broker-owned Langfuse egress that keeps durable provider credentials inside the broker, reauthorizes each session resource, validates a bounded telemetry projection, records durable pre-egress intent, and fails remote delivery without losing local run history.
- Hard limits for verification output, retry count, command evidence size and retention, and remote telemetry payload and delivery rates.
- Small non-overwriting global guidance and one optional audit skill in generic and project containers.
- Executable contracts around the credential broker, provider capabilities, launcher invariants, telemetry ingestion and egress policy, package dependencies, bounded quality evidence, docs examples, devcontainer readiness, project-devcontainer readiness, and persistent Codex and Claude login state across container replacement.

The repository cannot yet claim:

- Complete prevention of intentional credential or network bypass by an agent.
- Zero-to-productive single-command onboarding from a clean host.
- Live browser-based Claude OAuth sign-in and token refresh on a clean host (only offline persisted-login reuse across container replacement is validated).
- Complete cost accounting across every provider, ready-made Langfuse dashboards, or meta-agent analysis.
- Project-aware secret/cache/service/port provisioning.
- Autonomous project planning, context budgeting, model/tool choice, review, merge, or remediation.
- End-to-end observability for token spend, resource use, traces, and recurring failures.
- North-star maturity.

## North-Star Capability Map

| Capability | Current state | Next product proof |
|---|---|---|
| Governed project flows | Broker sessions, policy, wrappers, project overlay. | A project manifest that declares allowed agents, contracts, secrets, services, approval points, and run modes; enforced by `ai-agent up --project` and `ai-agent run`. |
| Security first | Strong supported-path auth controls, broker-retained durable provider secrets, policy-gated telemetry egress, and audit logs. | Decide the enforcement boundary for adversarial/confused agents: isolated per-run home, general egress policy, real-tool removal, or explicitly documented trust limit. Then test it end to end. |
| Simple use first | `ai-agent up` guides missing default config, starts the broker, enters the devcontainer, explains persistent Claude/Codex login state, `ai-agent auth status` reports login state with remediation, and both Codex and offline Claude login reuse are tested across container replacement. | Add clean-host E2E install and a live browser-OAuth smoke path so first login and re-entry are repeatable without source knowledge. |
| Executable quality contracts | Repo-local tests and gates, bounded `ai-agent check` evidence, readiness suites, and `--verify-cmd`. | Project-declared contract runner with structured, quiet results, failure classes, retry guidance, and persisted run history. |
| Adaptive efficiency | Managed-run telemetry records project, agent, model evidence, outcomes, duration, bounded retries, and provider-reported request usage. Verification and evidence output are capped, and optional sanitized OTLP delivery is brokered and bounded. | Validate usage and cost coverage, then add resource metrics, dashboards, and meta-agent recommendations. |
| Meta-agent layer | Not implemented. | Local analyzer that reads run telemetry across projects and emits recurring-failure patterns, waste reports, and concrete workflow changes. |

## Sharp Next Steps

1. Validate native usage and cost coverage across supported login and provider modes. Preserve source, scope, precision, and confidence. Keep missing values empty.

2. Build the first advisory meta-agent report. Read normalized run history across projects. Report repeated failures, retry waste, high token runs, and weak verification contracts. Do not mutate projects automatically.

3. Replace ad hoc verification with project contracts. Keep the current output and retry limits. Add structured failure classes and project-defined checks.

4. After the usage and telemetry milestone, return to the broader backlog. The next tranche should cover portable distribution, project manifests, stronger containment decisions, and autonomous planning/review.

## Completion Rule

A gap leaves this document only when the end-user behavior is implemented on the supported path, documented accurately, and validated by an executable test that would fail if the behavior regressed. Infrastructure alone, labels alone, or aspirational documentation do not close a product gap.
