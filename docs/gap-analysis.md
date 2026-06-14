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
| P0 | Project skill packs, reusable rules, memory extraction, context budgeting, model/tool selection, the operator cockpit, and meta-agent optimization are not implemented. | Product | North star |
| P1 | Readiness does not validate the complete user journey: install/setup, agent login, real GitHub push/PR, restart/re-entry, and project tooling. | Testing | Usable today, north star |
| P1 | Langfuse deployment exists, but agent instrumentation, Git notes, scoring, trace identity enforcement, and workflow dashboards are not shipped. | Observability | North star |
| P1 | PR automation classifies tiers only; automatic review, T1 auto-merge, post-merge auto-revert, event logging, and risk escalation are absent. | Automation | North star |
| P1 | Verify/retry restarts the same command without structured failure context, and local documentation checks silently skip when tools are missing. | Quality | North star |
| P2 | Reproducibility claims exceed implementation: base image tags and apt packages are mutable, downloaded artifacts lack checksum verification, and npm transitive dependencies are not locked. | Supply chain | Reliability |
| P2 | Product documentation and readiness language must remain bounded to implemented behavior and must not describe infrastructure presence as an end-user capability. | Product truth | Usable today, north star |

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
