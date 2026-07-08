# Product Gap Analysis

This is the source of truth for the work required to move AI Crew localdev from its current brokered-devcontainer foundation toward the north star:

> Autonomous, efficient, adaptive local dev environment: agents work inside governed project flows, security and simplicity are first priorities, quality is enforced through executable contracts, and a meta-agent layer monitors cross-project efficiency, resource use, token spend, and recurring failure patterns.

Reviewed 2026-07-08 after Claude login-state persistence with `ai-agent auth status` merged and an architecture extensibility review covering provider interception, distribution shape, and the adaptive feedback loop.

Work in this document is sequenced by five factors only: minimized code size, clarity and maintainability, user simplicity, security strictness, and local resource consumption. Implementation effort is not a ranking factor.

## Current State

The repository is no longer just an auth sketch. It has a working Linux-first foundation:

- `ai-agent up` is the primary entrypoint after installation: it guides missing default configuration, starts or finds the broker, runs host readiness checks, launches the generic devcontainer, and supports `--project` for a repository-owned devcontainer with a broker overlay, including compose-backed project devcontainers.
- The broker owns policy enforcement, provider registration, GitHub App signing and token minting, Langfuse telemetry egress, same-UID peer checks, rate limits, in-memory token caching, session state, and JSONL audit events. Durable GitHub and Langfuse secrets remain inside the broker process.
- `ai-agent run` creates a broker session, assigns a stable run ID, writes inspectable managed-run telemetry, optionally relays a sanitized OTLP projection through the authenticated broker session, passes the bind secret through an inherited FD, scrubs ambient GitHub, SSH, OpenTelemetry, and Langfuse credentials, sets fail-closed git config, shims `gh`, supervises the agent process, runs verification when requested, and revokes the session on exit.
- Native Claude and Codex usage collection is independent of optional Langfuse export. Authentication-independent runtime contracts cover Claude stored OAuth and API-key modes plus Codex ChatGPT and API-key modes, while request fixtures prove normalized provider usage and explicit missing cost.
- `ai-agent runs analyze` reads retained history across projects and emits deterministic usage and cost coverage plus advisory findings for recurring failures, retry waste, project-level high-token patterns, successful runs with missing or lower-quality usage, and ratio-based weak verification. Its lookback, thresholds, evidence count, and finding count are emitted budgets; verification advice precedes token-volume advice, and it never mutates projects or policy.
- The generic devcontainer is hardened for the supported path: reduced privileges, read-only root, broker socket mount validation, persistent agent home volume, and brokered `gh`/git tooling. `ai-agent up` explains the Claude/Codex first-login and re-entry flow, `ai-agent auth status` reports each agent's login state with remediation, and real Codex login plus both offline Claude login paths (an `apiKeyHelper` and a persisted OAuth credentials file) are proven to persist and be recognized across container replacement.
- Onboarding has improved: `ai-agent setup` can generate identities and policy, `ai-agent install` writes user systemd units, and non-interactive setup paths exist.
- Executable contracts exist for broker API shape, policy validation, provider capability registration, broker-owned telemetry egress, session invariants, launcher auth scrubbing, memfd behavior, authentication-independent native usage coverage, bounded adaptive analysis, package dependency boundaries, bounded quality evidence, devcontainer readiness, persistent Codex login state, persistent Claude login state across container replacement, the `ai-agent auth status` login probe, project-devcontainer readiness, docs examples, ADR gating, semantic identifier checks, and a self-documenting source policy.

This is still not the north-star product. It is a governed credential and container substrate with useful first slices of daily workflow support.

## Near-Term Milestone

The next milestone is to start using the tool in real work while making it self-evolving and cost efficient. That refocuses the immediate product work on:

1. Continued reduction of first-use friction beyond the current guided `ai-agent up` path, especially portable installation converging on a single multi-call artifact, agent login provisioning, and clean-host verification.
2. Build on the local adaptive report and brokered remote export with a durable findings ledger, measured recommendation outcomes, resource metrics, and dashboards.
3. Token and output discipline by default: live run-level token budgets with deterministic warn and stop behavior, visible token/cost monitoring, concise default agent guidance, quiet verification output, and project conventions that reduce noisy context before deeper automation is built.

## Priority Gaps

| Priority | Gap | Current evidence | Scope blocked |
|---|---|---|---|
| P1 | End-to-end readiness does not prove the full user journey. | Tests cover broker/devcontainer/project-devcontainer slices with mocked GitHub behavior, compose-backed project containers, brokered git/`gh`, ambient credential rejection, and real Codex login reuse. They do not install from an artifact on a clean host, perform real GitHub push/PR behavior, perform live Claude OAuth, or exercise restart/re-entry through the full user-facing CLI journey. | Product confidence, release readiness |
| P1 | Project runtime support is only a first slice. | `ai-agent up --project` honors a project devcontainer, injects a read-only broker/toolchain overlay, preserves project PATH/env, and has E2E coverage for compose services, ports, brokered git/`gh`, and ambient credential rejection. The project manifest now declares quality contracts, an enforced agent allowlist, and per-agent model defaults consumed by `ai-agent run`. It does not yet declare secrets, caches, service policy, ports, or approval points, and portable toolchain delivery is still bind-mounted from the host. | Daily development, multi-project use |
| P1 | Governance is enforced mainly through the broker, wrappers, environment scrubbing, and PATH control. A determined or confused agent can still bypass supported-path policy by using reachable real tools, stored credentials, raw network calls, or project-provided binaries. | Durable GitHub and Langfuse secrets stay in the broker, telemetry egress is independently authorized and validated, `ai-agent run` scrubs and shims the intended process tree, the generic image moves real `gh` off PATH, and project mode injects wrapper tooling. There is no general network egress policy, no isolated per-run home when that boundary is required, and no lower-level runtime enforcement boundary. | Security, governed flows |
| P1 | Installation and distribution still require a source checkout and local build. | The toolchain is now one multi-call `ai-agent` binary plus invocation-name symlinks (ADR 0012): `make install` copies a single artifact, project mode bind-mounts it at each interposed name, and the generic image symlinks it. No release artifact, install script, checksum-verified package, or published devcontainer Feature exists yet. | Simplicity, clean-host onboarding, portable project mode |
| P2 | Adaptive findings are not durable and resource budgets are retrospective only. | `ai-agent runs analyze` re-derives and re-prints the same advice on every invocation; nothing records whether a recommendation was accepted, dismissed, or improved outcomes. Provider-reported usage streams through the native relay during the run, but no run-level token budget acts on it, so overspend is only visible after the fact. | Adaptive efficiency, resource discipline |
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
- Documented Claude/Codex first-login and re-entry in the generic devcontainer, with real Codex login reuse and both offline Claude login paths (`apiKeyHelper` and a persisted OAuth credentials file) proven to persist and be recognized across container replacement, an `ai-agent auth status` login probe with remediation, and GitHub repo credentials kept on the brokered path.
- First-slice project devcontainer support through a read-only broker/toolchain overlay, including compose-backed project devcontainers.
- Inspectable managed-run history with stable run and task IDs, versioned metadata, model attribution, verification attempts, optional brokered OTLP export, and broker audit correlation.
- Native Claude and Codex usage capture with request-level provider attribution.
- Authentication-independent telemetry coverage contracts for Claude stored OAuth and API-key modes and Codex ChatGPT and API-key modes.
- A bounded advisory meta-agent report over retained cross-project history, with explicit policy, coverage, evidence, recommendations, and non-mutation behavior.
- Broker-owned Langfuse egress that keeps durable provider credentials inside the broker, reauthorizes each session resource, validates a bounded telemetry projection, records durable pre-egress intent, and fails remote delivery without losing local run history.
- Hard limits for verification output, retry count, command evidence size and retention, and remote telemetry payload and delivery rates.
- Small non-overwriting global guidance and one optional audit skill in generic and project containers.
- Manifest-declared quality contracts executed in order on managed runs, with fail-closed manifest validation, per-contract retry policy, deterministic failure classes, bounded retained evidence, and per-contract results in run history.
- A manifest-declared agent allowlist refused fail-closed before session creation, with per-agent model defaults recorded in run attribution only (they do not change the launched agent command or environment).
- Executable contracts around the credential broker, provider capabilities, launcher invariants, telemetry ingestion and egress policy, authentication-independent native usage coverage, bounded adaptive analysis, package dependencies, bounded quality evidence, docs examples, devcontainer readiness, project-devcontainer readiness, and persistent Codex and Claude login state across container replacement.

The repository cannot yet claim:

- Complete prevention of intentional credential or network bypass by an agent.
- Zero-to-productive single-command onboarding from a clean host.
- Live browser-based Claude OAuth sign-in and token refresh on a clean host (only offline login-state persistence and local recognition across container replacement is validated, not a provider-backed authenticated request).
- Complete cost accounting where providers omit cost, ready-made Langfuse dashboards, resource metrics, or automatic application of meta-agent recommendations.
- Project-aware secret/cache/service/port provisioning.
- Autonomous project planning, context budgeting, model/tool choice, review, merge, or remediation.
- End-to-end observability for token spend, resource use, traces, and recurring failures.
- North-star maturity.

## North-Star Capability Map

| Capability | Current state | Next product proof |
|---|---|---|
| Governed project flows | Broker sessions, policy, wrappers, project overlay, and a project manifest whose quality contracts and agent allowlist are enforced by `ai-agent run` fail-closed; manifest model defaults set run attribution only and do not change the launched agent command or environment. | Extend the manifest to declared secrets, services, caches, ports, approval points, and run modes, enforced by `ai-agent up --project` and `ai-agent run`, and make model defaults drive agent invocation rather than attribution alone. |
| Security first | Strong supported-path auth controls, broker-retained durable provider secrets, policy-gated telemetry egress, audit logs, and provider-declared interception profiles composed by the launcher with per-profile fail-closed invariant tests. | Decide the deeper enforcement boundary for adversarial/confused agents: isolated per-run home, general egress policy, real-tool removal, or explicitly documented trust limit — and test it end to end. |
| Simple use first | `ai-agent up` guides missing default config, starts the broker, enters the devcontainer, explains persistent Claude/Codex login state, `ai-agent auth status` reports login state with remediation, and both Codex and offline Claude login-state persistence are tested across container replacement. | Add clean-host E2E install and a live browser-OAuth smoke path so first login and re-entry are repeatable without source knowledge. |
| Executable quality contracts | Manifest-declared contracts run in order on every managed run with quiet passing output, bounded failure evidence, deterministic failure classes, per-contract retry policy (`agent` or `never`), and per-contract results persisted in run history; `--verify-cmd` remains as an explicit per-run override sharing the same bounded execution path. | Measure contract outcomes across runs and feed them into adaptive retry planning through the findings ledger. |
| Adaptive efficiency | Managed-run telemetry records project, agent, model evidence, outcomes, duration, bounded retries, and provider-reported request usage independently of remote export. The analyzer emits coverage, cost totals when reported, and bounded workflow recommendations. | Enforce run-level token budgets live through the existing native usage relay with an explicit warn threshold and deterministic stop policy, add resource metrics and dashboards, then measure whether accepted recommendations reduce tokens, retries, and failures. |
| Meta-agent layer | A deterministic local advisory analyzer reads retained cross-project history and reports recurring failures, retry waste, aggregated high-token patterns, distinct missing and lower-quality usage gaps, and ratio-based weak verification without mutation. | Persist findings in an atomically written ledger keyed by a stable finding fingerprint with acceptance status and a metric snapshot at acceptance, and report measured deltas for accepted recommendations before any automated project change. Any future LLM analysis consumes the bounded deterministic report, never raw history. |

## Sharp Next Steps

1. Validate live browser-based Claude OAuth sign-in and token refresh on a clean host, extending the existing offline login-state persistence and `ai-agent auth status` coverage without mixing personal agent state with governed repository credentials.

2. Publish a checksum-verified release artifact and install script for the single multi-call binary, so clean-host installation stops requiring a source checkout.

3. Make the adaptive loop act. Persist analyzer findings in an atomically written ledger with a stable fingerprint, acceptance status, and a metric snapshot at acceptance; report measured deltas for accepted recommendations; and enforce run-level token budgets live through the native usage relay with an explicit warn threshold and deterministic stop policy. Keep the analyzer a pure function of retained history with persistence beside it, and split `internal/platform/telemetry` along its model, store, relay, and export seams when resource metrics land.

4. Add resource metrics and dashboard views, then track recommendation acceptance and compare subsequent token, retry, failure, and quality outcomes.

5. Continue the broader backlog with single-artifact multi-call distribution, project manifests, stronger containment decisions, and autonomous planning/review.

## Completion Rule

A gap leaves this document only when the end-user behavior is implemented on the supported path, documented accurately, and validated by an executable test that would fail if the behavior regressed. Infrastructure alone, labels alone, or aspirational documentation do not close a product gap.
