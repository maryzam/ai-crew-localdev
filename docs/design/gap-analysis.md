# Product Gap Analysis

This is the long-lived source of truth for the gap between the current product and the north star: an autonomous, efficient, adaptive local development environment where agents work inside governed project flows, quality is enforced through executable contracts, and local evidence drives resource discipline and workflow improvement.

This document records product gaps only. Low-level implementation details, command flags, test names, package moves, and migration sequencing belong in code, ADRs, or pull requests.

## Current Product State

- AI Crew localdev is a Linux-first governed agent workspace foundation with one multi-call binary, a host broker for durable provider secrets, guided setup and `up`, brokered `git` and `gh` access, managed devcontainer entry, managed runs, bounded verification, local run history, optional broker-authorized telemetry export, native Claude and Codex usage capture, live token warn/stop budgets, and advisory adaptive findings with a durable local ledger.
- Managed runs now follow the intended control-plane shape for the supported path: CLI flags become a request, the planner resolves project intent and host constraints into a `RunPlan`, and the runtime executes that plan mechanically. Planning failures happen before broker session creation and credential minting.
- Project manifests are now the project operating-model declaration surface. The supported schema is `ai-agent-manifest/v2`, covering agents, quality contracts, brokered resources, caches, services, ports, approvals, run modes, and token resource budgets. Managed runs enforce allowed run mode, supported approvals, declared broker resources, broker-authoritative resource preflight, and resource budgets before broker session creation; `up --project` enforces project-devcontainer run mode, adds declared cache volumes and ports, includes declared compose services, and rejects cache targets that overlap reserved ai-agent paths.
- The supported execution path is container-first. Native host execution is not a product claim for managed runs.
- The broker remains the credential and audit boundary. Durable GitHub and Langfuse secrets stay host-side; workspaces receive scoped session capabilities and brokered tools.

## Remaining North-Star Gaps

| Priority | Gap | Current boundary | North-star proof needed |
|---|---|---|---|
| P1 | Containment is still confused-agent containment, not adversarial containment. | The broker owns durable secrets, managed runs scrub ambient credentials, brokered tools fail closed, and isolated run homes hide personal home-relative credential state on the supported path. | A deliberate containment decision is implemented and tested: network egress policy, real-tool removal, stronger runtime isolation, or a documented non-goal with explicit trust limits. |
| P1 | First-use flow is guided, not zero-to-productive. | Release artifact install, guided setup, broker startup, doctor, generic devcontainer entry, and clean-host journey coverage exist. | A new operator can install, configure required provider access, enter a workspace, sign into agent CLIs, and complete a brokered managed run with fewer manual steps and release-level smoke coverage. |
| P2 | Autonomous workflow orchestration does not exist. | Runs are operator-triggered and adaptive findings are advisory. | A policy-gated planner can choose tasks, context, agent/model/tool, approval points, quality gates, review, merge, and remediation steps from project and host declarations. |
| P2 | Adaptive recommendations are not yet applied through the system. | Findings persist with status and measured outcome deltas, but accepted advice does not update manifests, guidance, budgets, or workflows. | Accepted recommendations create explicit, reviewable changes through the same governed project flow and later analysis measures whether they reduced tokens, retries, failures, or weak verification. |
| P2 | Observability is useful but not an operator cockpit. | Local run history, usage, budget threshold events, optional trace export, and advisory analysis exist. | Operators get a compact local view of active runs, spend, repeated failures, resource pressure, quality status, and accepted recommendation outcomes without reading raw event files. |
| P2 | Supply-chain reproducibility is incomplete. | The release artifact is checksum-verified and the devcontainer uses pinned versions where practical. | Runtime images and downloaded tools are reproducible enough for security claims: base images, packages, and fetched artifacts have auditable versions and integrity checks. |
| P3 | Documentation freshness is manually governed. | Architecture and gap truth are consolidated here and in `docs/design/architecture.md`, while user docs remain hand-maintained. | User-facing examples and architecture claims are covered by scenario tests or generated checks where they affect security, lifecycle, budgets, or supported workflows. |

### Security-hardening proofs tracked under the gaps above

These operator-facing hardening steps are concrete proofs for the gaps above, not separate gaps:

- Enforced always-containerized execution — the default agent CLIs refuse to run brokered work directly on the host — is part of the P1 containment decision.
- Proactive audit-log review that flags anomalies, and unused-resource pruning that suggests dropping `resources` entries unused for 30 days, are proofs for the P2 observability cockpit and applied-recommendations gaps.
- PEM key hygiene is already enforced: the broker refuses group- or world-readable keys, and `ai-agent doctor` surfaces permission failures and past-due rotation before a session starts.

## Closed Migration Gaps

- The heavy CLI to control-plane move is no longer an active product gap for managed runs. The remaining work is simplification and broader product capability, not migration tracking.
- The first project operating-model gap is closed for the supported declaration surface. Project manifests can declare brokered resources, caches, services, ports, approvals, run modes, and token budgets; managed runs and `up --project` validate and consume those declarations through the control plane, broker preflight, and project devcontainer overlay rather than leaving them as documentation-only intent.
- Live run-level token budgets are no longer only retrospective. They are planned from CLI input, enforced from native usage events, emit local evidence, and fail closed when a hard stop cannot be enforced.
- Provider registration, interception declarations, quality contracts, retry policy, project manifest intent, and adaptive findings are no longer scattered roadmap ideas; they are current architecture surfaces with remaining product expansion work.

## Claim Boundaries

The repository can claim a governed local substrate for AI coding agents: broker-retained durable provider secrets, scoped GitHub credentials, fail-closed brokered tooling on the supported path, project-aware managed runs, project operating-model manifest enforcement for supported declarations, bounded quality evidence, local run history, native usage capture, live token budgets, and advisory adaptive findings.

The repository cannot yet claim adversarial agent containment, complete project environment provisioning, autonomous task planning or merge automation, zero-touch onboarding, complete cost accounting when providers omit cost, a full operator cockpit, or north-star maturity.

## Completion Rule

A gap leaves this document only when the end-user behavior is implemented on the supported path, documented accurately, and validated by an executable check that would fail if the behavior regressed. Infrastructure alone, labels alone, or aspirational documentation do not close a product gap.
