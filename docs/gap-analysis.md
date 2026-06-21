# Product Gap Analysis

This is the single source of truth for work required to make AI Crew localdev
usable for daily local development and to reach the product north star.

The current repository is a credible Linux GitHub-auth broker foundation. It is
not yet a self-contained daily development environment or an autonomous,
self-correcting local development control plane.

## Prioritized Gaps

| Priority | Gap | Type | Scope blocked |
|---|---|---|---|
| P0 | Managed sessions can bypass broker policy by invoking the real `gh` binary and using stored personal authentication. | Security | Usable today, north star |
| P0 | Agent login, configuration, and state have no supported provisioning or persistence; the container home is ephemeral. | Runtime/UX | Usable today |
| P0 | Onboarding still requires a source checkout, local build, one GitHub App and PEM per agent, interactive setup, and broker service installation. | Installation | Usable today |
| P0 | The generic image does not provision project-specific runtimes, dependencies, services, secrets, ports, caches, or project devcontainer configuration. | Product/Runtime | Usable today, multi-project |
| P0 | Session binding secrets are persisted in runtime JSON files, and normal exec-based sessions are not automatically revoked or cleaned up when the agent exits. | Security/Reliability | Usable today, north star |
| P0 | North-star product capabilities are not implemented: project-aware agent guidance, a local workflow dashboard, and adaptive cross-project optimization. | Product | North star |
| P1 | Readiness does not validate the complete user journey: install/setup, agent login, real GitHub push/PR, restart/re-entry, and project tooling. | Testing | Usable today, north star |
| P1 | Langfuse deployment exists, but agent instrumentation, Git notes, scoring, trace identity enforcement, and workflow dashboards are not shipped. | Observability | North star |
| P1 | PR automation classifies tiers only; automatic review, T1 auto-merge, post-merge auto-revert, event logging, and risk escalation are absent. | Automation | North star |
| P1 | Verify/retry restarts the same command without structured failure context, and local documentation checks silently skip when tools are missing. | Quality | North star |
| P2 | Reproducibility claims exceed implementation: base image tags and apt packages are mutable, downloaded artifacts lack checksum verification, and npm transitive dependencies are not locked. | Supply chain | Reliability |
| P2 | Product documentation and readiness language must remain bounded to implemented behavior and must not describe infrastructure presence as an end-user capability. | Product truth | Usable today, north star |

## North-Star Glossary

These terms describe intended product capabilities, not current behavior.

| Term | Meaning |
|---|---|
| Project skill packs | Reusable, project-type-specific rules and prompts that give agents stack-aware defaults for common repositories. |
| Memory extraction | Capturing stable project facts from prior sessions so agents do not repeatedly rediscover the same context. |
| Context budgeting | Selecting and trimming project context deliberately so agent runs stay relevant and token use stays bounded. |
| Model/tool selection | Guidance or automation for choosing the right agent, model, and tools for a task based on risk, cost, and project state. |
| Operator cockpit | A local dashboard for active runs, agent status, diffs, tests, PR state, approvals, traces, token use, and resource use. |
| Meta-agent optimization | Cross-project analysis that detects recurring failures, waste, idle loops, bad model choices, and coaching opportunities. |

## Claim Boundaries

The repository can currently claim:

- Linux-only GitHub App credential brokering for managed agent sessions
- repo-scoped broker policy and fail-closed wrappers on the intended command path
- a hardened generic devcontainer image with preinstalled agent CLI binaries
- unit, invariant, CI, and image-level readiness coverage for the broker foundation

The repository cannot yet claim:

- zero-to-productive single-command onboarding
- a persistent day-to-day development workspace
- complete prevention of personal credential bypass
- project-aware local environment provisioning
- end-to-end workflow observability or autonomous quality loops
- north-star maturity

## Completion Rule

A gap leaves this table only when its end-user behavior is implemented, covered
by the supported workflow, and validated by an end-to-end test. Adding
infrastructure, a label, a configuration file, or documentation alone does not
close a product gap.

## Status and Sequencing

The four "usable today" P0 pull requests (#52–#55) have merged, but no gap is
retired yet: each still owes the end-to-end validation the Completion Rule
requires, which is why the prioritized-gaps table is untouched.

| PR | Shipped | Validation still owed |
|---|---|---|
| #52 | Session credential integrity: bind secret no longer persisted, sessions revoked on agent exit, unmanaged `gh` moved off `PATH`. | End-to-end proof that brokered `git push` and `gh` both work while an ambient personal token is rejected. |
| #53 | Onboarding: `make install`, `ai-agent install` units, non-interactive `ai-agent setup`. | Artifact/image distribution (so install is not a source build) plus a clean-host journey test that installs from it. |
| #54 | Persistent agent home via a named volume. | Restart and re-entry test proving the home survives. |
| #55 | Project-aware provisioning, first slice: `ai-agent up --project` honors a project's own devcontainer and injects a read-only broker overlay. | Real-container test: broker reachable, brokered push, read-only overlay, shell entry on a minimal base. |

Remaining work is sequenced as:

- **Real-container end-to-end validation, then retire proven gaps.** Add an `integration`-tagged test in `internal/e2e/` that runs `ai-agent up --project` against a repo with its own devcontainer and asserts: the project's devcontainer (not the generic image) comes up with its `postCreateCommand`, compose service, forwarded port, and a project-only tool on `PATH`; the broker socket is reachable; a brokered `git push` and `gh` call succeed through the injected toolchain while an ambient personal token is rejected; overlay mounts are read-only on a minimal base. Give it a dedicated Make target (not folded into `readiness-devcontainer`), with Podman rootless as the primary acceptance path. Only after it passes against a real runtime, retire gaps that are fully implemented **and** validated — not the project-runtime P0 (waits on secrets/caches/services and portable toolchain) and not onboarding-P0 (waits on artifact distribution).
- **Portable toolchain injection via a devcontainer Feature.** Replace the host-binary bind-mount overlay with a Feature that installs the `ai-agent` toolchain inside the project container, removing the single-user/single-architecture assumption. Depends on deciding how the toolchain is published and fetched (release artifacts or an image; likely an ADR under `docs/decisions/`). Keep the read-only socket mount and `PATH` wiring; only binary delivery changes.
- **Project-declared secrets, caches, and service wiring.** Extend `ai-agent up --project` beyond what a project's devcontainer already expresses. Design the declaration format first (an ai-agent overlay manifest, not edits to the project's devcontainer) and how secrets reach the container through the broker rather than baked into an image. Largest, least-defined slice; propose a design and a thin first slice before implementing.

**Distribution prerequisite (gates onboarding-P0 claims):** `make install` is still a source checkout plus local build. Before onboarding-P0 can be claimed as progressing, add release artifacts or a pinned, checksum-verified image so a clean host can install the toolchain without cloning and building. This is the host-side counterpart of in-container toolchain distribution and likely shares the same decision record.
