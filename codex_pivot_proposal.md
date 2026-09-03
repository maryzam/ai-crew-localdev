# AI Crew localdev pivot proposal

Review date: 2026-09-03

Review basis: clean `main` at `de6d1c0`, matching `origin/main` at the time of review.

## Verdict

Pivot rather than abandon.

The repository's defensible value is no longer "a better local agent harness." Claude Code, Codex, and Gemini CLI are rapidly commoditizing that layer. The moat is a vendor-neutral governance boundary:

> Run any coding agent without giving it durable GitHub credentials, and produce local evidence of what it could access, spent, changed, and verified.

The broker, cross-agent evidence model, hard budgets, and high-assurance checkout are worth retaining. Autonomous orchestration, generic workspace UX, bundled observability infrastructure, and native-agent features are not good areas for further investment.

## 1. What the official harnesses have commoditized

| Area | Official capability now | Recommendation |
|---|---|---|
| Instructions and extensions | CLAUDE.md, AGENTS.md, GEMINI.md, skills, MCP, plugins or extensions, custom agents, and subagents are native across the ecosystem. Claude explicitly positions these as its extension layer; Codex has AGENTS.md, skills, subagents, MCP, and hooks; Gemini extensions bundle skills, policies, hooks, and preview subagents. [Claude](https://code.claude.com/docs/en/features-overview), [Codex](https://learn.chatgpt.com/docs/agent-configuration/subagents), [Gemini](https://geminicli.com/docs/extensions/reference/) | Stop building agent intelligence or guidance as core product functionality. A small portable skill pack can remain ancillary. |
| Sandboxing and approvals | Claude uses bubblewrap or Seatbelt with filesystem and domain controls; Codex has OS sandbox modes, approvals, rules, and network policy; Gemini supports Docker, Podman, tool sandboxing, and a policy engine. [Claude](https://code.claude.com/docs/en/sandboxing), [Codex](https://learn.chatgpt.com/docs/agent-approvals-security), [Gemini](https://geminicli.com/docs/cli/sandbox/) | Let native harnesses own ordinary command safety. Retain only controls connected to brokered privileges and the stronger high-assurance mode. |
| Lifecycle interception | All three have lifecycle hooks. Claude and Gemini can reject responses and trigger retries; Codex hooks support validation at turn stop. [Claude](https://code.claude.com/docs/en/hooks-guide), [Codex](https://learn.chatgpt.com/docs/hooks), [Gemini](https://github.com/google-gemini/gemini-cli/blob/main/docs/hooks/reference.md) | Implement quality contracts through native hooks where practical instead of owning more agent-loop machinery. |
| Parallelism and sessions | Claude has CLI worktrees, subagents, teams, `/batch`, background agent view, cloud tasks, and routines. Codex has native subagents, background threads, non-interactive execution, and worktree flows in its broader product surface. [Claude](https://code.claude.com/docs/en/agents), [Codex](https://learn.chatgpt.com/docs/non-interactive-mode) | Do not pursue the repository's proposed autonomous planner, scheduler, or multi-agent cockpit. |
| Telemetry | Claude, Codex, and Gemini emit native OpenTelemetry or structured usage data. Claude includes costs, tools, decisions, and identity; Gemini emits token, agent, tool, and performance data; Codex exposes conversation, request, tool, and approval events. [Claude](https://code.claude.com/docs/en/monitoring-usage), [Codex](https://learn.chatgpt.com/docs/agent-approvals-security), [Gemini](https://geminicli.com/docs/cli/telemetry/) | Collection is commodity. Cross-vendor normalization, local privacy, correlation, and live enforcement remain useful. |
| Development environments | Native sandboxes, custom images, setup scripts, cloud environments, and worktree setup increasingly cover this. | Stop expanding manifest-owned caches, ports, services, and general environment provisioning. Prefer `devcontainer.json` or each harness's native environment contract. |
| Dashboards and hosted automation | Claude has agent view, cloud sessions, and routines; Codex has analytics, compliance APIs, and remote or long-running work. | Do not build a general operator cockpit or hosted automation competitor. |

The current image also demonstrates the cost of owning the distribution layer: it pins Claude `2.1.84` and Codex `0.116.0` in [`.devcontainer/Dockerfile`](.devcontainer/Dockerfile), while the review host already had `2.1.251` and `0.151.0`. This is not inherently incorrect because pinning supports reproducibility, but it means the project continually trails and retests fast-moving upstream harnesses.

## 2. What is worth retaining

### The GitHub credential broker

This is the clearest differentiator.

The broker keeps the GitHub App key outside the agent, mints repository-scoped short-lived credentials, denies cross-repository requests, blocks ambient fallback, and requires durable audit intent before issuance. That is materially different from a normal CLI permission prompt or sandbox. The exact boundary is documented and backed by executable proofs in [`docs/design/security-design.md`](docs/design/security-design.md).

Claude's cloud offering has a secure credential proxy, but that is vendor-hosted and Claude-specific. This repository can provide the same class of local boundary consistently to Claude, Codex, and Gemini.

### Cross-vendor contracts and evidence

The manifest joins allowed agents, broker resources, verification, and budgets before privileged side effects. Keep those governance fields and trim general environment provisioning from the schema. See [`internal/configmodel/manifest/types.go`](internal/configmodel/manifest/types.go) and [`docs/guide/quality-gates.md`](docs/guide/quality-gates.md).

The normalized event model is more valuable than any particular OpenTelemetry receiver because it correlates:

- Repository and commit
- Agent and model attribution
- Broker session and privileges
- Verification result
- Retry waste
- Provider-reported usage
- Runtime outcome

That join is visible in [`internal/platform/runevents/types.go`](internal/platform/runevents/types.go). Vendor dashboards generally see their own model activity, not this complete governance chain.

### Hard, cross-agent run budgets

Claude has a print-mode dollar ceiling, while the other native surfaces mainly expose statistics, quotas, or context limits. The repository's provider-neutral token warn and stop policy, backed by native telemetry and failing closed if enforcement cannot start, remains useful. See [`docs/guide/quality-gates.md`](docs/guide/quality-gates.md).

### High-assurance private checkout as an optional mode

The latest change added about 6,300 lines, so this is now a significant maintenance centre. Nevertheless, it is stronger than a normal worktree:

- Independent `.git`, object store, refs, and configuration
- Human checkout and siblings are not mounted
- Dirty and ambiguous source states are refused
- Result application is explicit and fast-forward-only
- Interrupted transitions retain evidence and reconcile deterministically

Those properties are described in [`docs/decisions/0019-governed-repository-sessions.md`](docs/decisions/0019-governed-repository-sessions.md). Retain this as a high-assurance governed mode, not as the only way to run an agent.

## 3. Cheap, useful pivot

### Recommended product: agent governance gateway

The smallest compelling surface is:

1. Select any installed Claude, Codex, or Gemini CLI.
2. Apply one host-owned policy.
3. Broker only approved GitHub operations.
4. Enforce a run budget and deterministic quality contract.
5. Emit a portable governance receipt.

A realistic first increment is roughly two engineer-weeks:

| Increment | Estimate | Proof of completion |
|---|---:|---|
| Complete Gemini support | 3-5 days | Gemini passes the same login-state, telemetry, budget, and brokered GitHub end-to-end contract as Claude and Codex. Much attribution groundwork already exists in [`internal/platform/modelattrib/modelattrib.go`](internal/platform/modelattrib/modelattrib.go), while the actual capability registry still contains only Claude and Codex in [`internal/agents/capabilities/capabilities.go`](internal/agents/capabilities/capabilities.go). |
| Add a bounded governance receipt | 3-5 days | `ai-agent runs receipt <id> --json` and `--markdown` record the policy digest, repository, base and result commits, granted resources, agent and model, budget, usage, and verification. Receipts exclude prompts and credentials and have a hard size limit. Most fields already exist. |
| Add a native-harness compatibility audit | 2-3 days | One command reports installed and pinned versions, available sandbox, hook, and OpenTelemetry capabilities, conflicting native configuration, and which manifest guarantees are enforced versus advisory. |
| Make bundled Langfuse opt-in | 1-2 days | Default startup uses bounded local JSONL or a user-supplied OpenTelemetry destination and starts no database services. |

The receipt is the strongest cheap addition. It converts existing internals into something teams can attach to a pull request, CI artifact, or audit record:

```text
result commit -> agent/model -> granted repository scope
              -> token budget/actual usage
              -> quality contracts/outcomes
              -> policy and runtime digests
```

Later, the same receipt can be signed or published as a GitHub check without changing its schema.

## What to stop now

- Do not implement the autonomous planner described in the current north star.
- Do not build a general multi-agent scheduler or cockpit.
- Do not expand local Langfuse orchestration; keep it as an adapter.
- Do not add more project environment semantics to the custom manifest.
- Do not recreate native hooks, skills, worktrees, policy engines, or session managers.
- Do not weaken the broker claim to obtain a simpler native mode. Until the documented same-UID and runtime-identity gap is solved, brokered write access should remain in governed mode. The limitation is already recorded in [`docs/design/gap-analysis.md`](docs/design/gap-analysis.md).

## Recommended positioning

Narrow the product from "autonomous local development environment" to "portable governance and evidence for native coding agents." That product is smaller, cheaper, and much harder for any one model vendor to obsolete.

## Checks performed during the review

Focused tests passed for capability mapping, manifests, immutable run plans, the GitHub provider, private workspace lifecycle, and adaptive analysis. No product source files were changed as part of the review.
