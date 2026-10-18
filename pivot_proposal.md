# AI Crew localdev pivot

## Decision

AI Crew localdev becomes a secure local substrate for native coding agents, not another coding-agent harness.

Its core outcome is:

> Start Claude Code or Codex in one command inside an isolated rootless Podman environment, let the agent work freely on the project, prevent durable or excessive access to external systems, and persist enough correlated evidence to understand and improve the work later.

Claude Code and Codex run unmodified and retain their native user experience, reasoning, sessions, subagents, skills, hooks, reviews, and context management. AI Crew localdev supplies the vendor-agnostic unification and durability layer around them: a common launch boundary, external capabilities, evidence, shared guidance, and later cross-agent flows and improvement analysis.

This layer stays lean and should shrink whenever native harnesses absorb a capability. It retains only the common behavior needed to bring the harnesses to a consistent operating baseline and the cross-agent capabilities that no individual harness can own.

## Change in intent

The previous design treated AI Crew localdev as a governed development workflow with agent identities, custom plans, managed runs, private workspaces, an apply step, budgets, and provider-specific orchestration.

That is no longer the product.

Native agent CLIs now provide the agent loop and extension mechanisms well enough. Recreating those features adds latency, maintenance, and cognitive load without strengthening the important boundaries. The new design composes with native CLIs through their supported hooks, skills, configuration, structured output, and OpenTelemetry interfaces.

The product is now deliberately narrower:

- Rootless Podman protects the host and limits the project-visible filesystem.
- A host capability broker holds GitHub, AWS, model-provider subscription, and future external authority and gives the container only placeholders or short-lived, provider-scoped credentials.
- Thin write wrappers route the few supported external writes to the host, while provider-side permissions remain authoritative.
- A local evidence pipeline correlates native telemetry, broker audit, container lifecycle, and repository state under one run ID.
- A shared unification layer gradually adds portable skills, cross-agent flows, and measured improvement without replacing either native runtime.

## Decision priorities

Within the non-negotiable security and evidence invariants, use this order to direct product investment and resolve competing improvements:

1. Durable traceability with broad correlated context and no silent loss of required evidence.
2. Fast autonomous progress toward useful outcomes with low human attention cost.
3. Simple, reliable operation with a small understandable surface.
4. Enforceable security appropriate to each project's risk surface, including protection from irreversible external actions.
5. Efficient token, cache, model, and infrastructure use.
6. Continuous, measured improvement of skills, rules, routing, and development flow.
7. Extensibility and compatibility with additional agents or legacy workflows.

Backward compatibility is not a reason to preserve the current architecture. Generality is added only after a second concrete use case proves it is needed.

Every operational trade-off must have a measurable budget, emitted evidence, and deterministic failure policy. Security and governance paths fail closed; optional observability destinations may fail without stopping local work because canonical evidence remains local.

## Ownership boundary

| Concern | Native Claude Code and Codex | AI Crew localdev |
|---|---|---|
| Reasoning, coding, planning, subagents, context, review | Own | Do not duplicate |
| Interactive and non-interactive agent UX | Own | Launch unchanged |
| Skills and project instructions | Load and execute | Curate and synchronize later |
| Tool lifecycle integration | Expose hooks and structured events | Install thin adapters |
| Model-provider login | Own the login flow and request format | Hold subscription credentials on the host; the container sees only placeholders |
| Project execution isolation | Secondary native safeguards | Rootless Podman is the default boundary |
| Network egress | Not owned | Open in v0, restricted mode in v0.5 |
| GitHub and AWS authority | Consume short-lived capabilities | Hold durable authority and enforce maximum scope |
| External writes | Request intent | Record, enforce scope, and execute on the host |
| Telemetry production | Emit native OTel and structured events | Correlate at the source, persist raw, fan out |
| Cross-agent flow and improvement | No shared ownership | Build later from measured local evidence |

Native hooks improve the interaction and can rewrite or annotate tool calls, but they are not the security boundary. An agent with broad access inside its container can bypass a wrapper. External security therefore comes from host custody, provider-enforced permissions, short lifetimes, repository protection, and host-mediated write actions.

## System boundary and provisioned scope

```mermaid
flowchart TB
    Human["Human: goal, guardrails, critical decisions"]

    subgraph V1["Vendor-agnostic unification — provisioned next"]
        Skills["Shared skills and project guidance"]
        Flow["Task and goal flow, review policy, scheduler, remote decisions"]
        Optimizer["Deterministic analysis + selective AI diagnosis + human-approved improvements"]
    end

    subgraph Host["Host boundary — immediate v0"]
        CLI["Thin ai CLI: init, up, logs"]
        Broker["Broker daemon: run-bound sockets, GitHub/AWS/model credential custody, write gateway, model proxy, audit-before-action"]
        Collector["OTel Collector"]
        Evidence["Per-run evidence directory: audit, raw OTel, lifecycle"]
    end

    subgraph Task["Disposable task container — immediate v0"]
        Agents["Native Claude Code + Codex CLIs"]
        Adapters["Credential helpers, git push and gh PR wrappers, hooks"]
        Project["Selected project + explicit caches"]
    end

    Views["Phoenix (v0) · Langfuse (v0.5)<br/>rebuildable views"]
    Providers["GitHub, AWS, Anthropic, OpenAI<br/>provider-enforced maximum authority"]

    Human --> CLI
    Human --> Flow
    CLI --> Agents
    Skills --> Agents
    Flow --> Agents
    Agents <--> Project
    Agents --> Adapters
    Adapters --> Broker
    Broker --> Providers
    Agents -->|"OTel, hook events"| Collector
    Collector --> Evidence
    Broker --> Evidence
    Collector --> Views
    Evidence --> Optimizer
    Optimizer -->|"proposed changes"| Human
```

The native CLIs own each agent loop. The task container is the execution boundary. The broker remains deliberately narrow: it holds external authority, applies deterministic capability policy, and durably records decisions before external actions. Shared prompts, goal shaping, review procedures, and project conventions belong in portable skills or generated native instructions rather than in broker code.

## Immediate v0

The first usable version contains only three foundations.

### 1. Easy rootless Podman environment

The initial operator surface is:

```text
ai init
ai up [--task <label>] [-- <native-command>]
ai logs [run-id]
```

`ai init` creates a small project profile and an optional project Containerfile. `ai up` validates its own prerequisites, builds or reuses the project image, starts or attaches to a task-scoped container, and connects the broker and telemetry endpoints. With no native command it opens a shell; for example, `ai up --task feature-a -- codex` starts Codex as the first process.

A run is one task container's lifetime. The first `ai up --task <label>` creates the container and its run ID; later `ai up` calls with the same label attach to that container and record an attach event in the same run. Replacing the container, for example after an image change, starts a new run that shares the task label. Native session IDs nest inside the run, so Codex as primary and Claude as reviewer in one container share one run. The run ID is the only identifier AI Crew introduces: it names the broker socket, the evidence directory, the OTel resource attribute, and the AWS source identity, so it uses letters, digits, and hyphens within 64 characters.

The task label is correlation context, not a v0 goal specification or an agent identity. Every supported native CLI is available in the same container, so Claude can later act as reviewer, advisor, or fallback for work started with Codex. V0 does not orchestrate those roles; the provisioned cross-agent flow does.

V0 allows one task container per checkout. A second task on the same checkout fails with a message to use another clone or worktree; v0 does not create or manage worktrees.

The container mounts only the selected project at `/workspaces/<project>`, explicitly declared caches, the dedicated Claude and Codex home volumes, and its own broker socket. The project-specific mount path keeps native session histories distinct per project. Each agent-home volume is the CLI's complete native config home, selected through its config-home variable so that all native state, including Claude's home-root settings file, lands inside the volume. Volumes are mounted as directories, never as single files, because the CLIs replace their files by atomic rename. The container never mounts the operator's home, host agent login state, SSH agent, AWS configuration, GitHub configuration, or sibling repositories. Dependency and agent-home volumes survive container replacement; the container itself is disposable.

Agent-home volumes hold native settings and session history but never real model-provider credentials; section 2 defines model-provider custody.

V0 egress is open, and each run records its egress mode. Exposure is bounded because every credential in the container is either a placeholder or short-lived and read-scoped. The v0.5 restricted mode replaces this with an allowlisted host proxy.

The agent has high trust inside the container and can install dependencies, run arbitrary project tools, and change project files without routine approval prompts. That trust does not extend to the host or external systems.

### 2. External capability boundary

Long-lived GitHub App keys, AWS refresh credentials, private keys, and equivalent authority remain on the host in one broker daemon, which `ai up` starts as a user service when it is absent.

`ai up` asks the broker for a run-bound socket before starting the container and mounts only that socket. The broker fixes the repository, branch prefix, and AWS role for the socket's lifetime and derives all authority from the socket a request arrives on; requests never name a repository or role. The socket is removed when the container stops. Rootless Podman maps container processes to the operator's UID, so peer credentials cannot tell containers apart and are not used for authorization.

The container obtains credentials through native helper interfaces backed by its socket. A git credential helper and the `gh` wrapper fetch a repository-scoped read-only GitHub token on demand, and AWS `credential_process` returns a short-lived STS session for the project-approved role. Helpers refresh on expiry, so no credential is stored in the container's persistent environment or on disk. AWS sessions use the run ID as source identity, deny role chaining, and are read-only by default. Provider-side permissions are the final maximum authority even if a helper or wrapper is bypassed.

Model-provider subscription credentials are host-held for both CLIs, and this is a v0 requirement. `ai login claude` and `ai login codex` run the native login in a short-lived login container whose volume only the broker reads; task containers never mount it. Claude uses a `claude setup-token` subscription token. The agent-home volumes contain only placeholder credentials, and each CLI reaches its provider through a model proxy behind the run socket, exposed on container loopback by a minimal forwarder where a CLI requires an HTTP URL. The proxy replaces the placeholder with the real credential and performs any token refresh on the host, so a placeholder copied out of the container cannot reach the provider. The proxy fails closed: if the broker is unavailable, agents cannot reach their models. Binding a model credential to a run is audit evidence; per-request model, token, cache, and rate-limit metadata is telemetry, and the proxy never records request or response content.

Subscription authentication formats are vendor internals, not published contracts. CLI versions are pinned in the image and upgraded only after an automated compatibility check against the model proxy passes.

A spike precedes the model proxy. It confirms that each CLI works with placeholder credentials and host-side refresh across a CLI upgrade without forged tokens, and that substituting credentials for the genuine CLI is consistent with each vendor's current consumer terms. If Codex cannot meet this, it is excluded from v0 until the decision is revisited; no CLI ever falls back to real credentials inside the container.

GitHub writes never receive a write token inside the container. The v0 host gateway supports only normal append-only pushes within the run's branch prefix, by default `ai/<task>/`, draft pull-request creation or update, and comments on the bound pull request. It resolves the repository and branch itself, rejects force pushes, branch deletion, unexpected refspecs, and non-descendant history, records authorization durably, then performs the action with a host-only short-lived credential.

V0 assumes the GitHub Free tier, where private repositories have no rulesets or branch protection, so the host gateway alone enforces push scope. Provider-side push rules are deferred until evidence shows the gateway is insufficient.

Published history is append-only. Review corrections are follow-up or revert commits; amend, rebase of published work, hard-reset removal, force push, and administrative bypass are outside the supported path.

Only `git push` and the supported `gh pr create`, `gh pr edit`, and `gh pr comment` invocations are wrapped. Each wrapped call has one of three deterministic results: run a permitted read, submit a supported host action, or refuse with a hint naming the supported path. The wrapper always states whether the original action occurred. Native hooks may give the same guidance earlier, but neither hooks nor wrappers are enforcement: a bypassed wrapper meets a read-only token. Host-action refusals are audit evidence; attempted commands are visible through hook telemetry.

### 3. Durable evidence and telemetry

Evidence has two classes with different guarantees.

Audit evidence covers credential issuance, policy decisions, refusals, and host actions. The broker appends it to the run's audit file and fsyncs before releasing a credential or performing an action; if the write fails, the action fails.

Telemetry covers native Claude and Codex OpenTelemetry signals and hook events. Native exporters do not wait for acknowledgement, so this class is best-effort: it is stored as received, and Collector receive and export failures are counted and shown by `ai logs`, never silently discarded.

Each run has one directory:

```text
runs/<run-id>/
  audit.jsonl   broker decisions and host actions, fsynced before release
  otel.jsonl    raw OTLP JSON from the Collector file exporter, grouped by run ID
  run.jsonl     container lifecycle, attach events, image, configuration, egress mode
```

Correlation happens at the source. `ai up` injects the run ID and task label into each native CLI's OTel resource attributes and hook environment, so every record carries them on arrival and no later join is needed. Telemetry is stored raw; mapping to a common session, turn, model, and tool shape happens at read or analysis time, so vendor schema changes never corrupt stored evidence.

Native session transcripts stay in the agent-home volumes and are referenced by session ID. Copying prompt or response content into run evidence is opt-in, redacted where configured, and retention-bounded.

The Collector also sends the stream to Phoenix, the v0 view. Phoenix is rebuildable and not the source of truth; a run remains inspectable through `ai logs` when Phoenix or the network is unavailable.

Before the view path is built, a spike confirms per CLI which signals are emitted, spans or metrics and log events, and which mechanism carries the run ID: resource-attribute environment, OTLP headers, or per-run configuration. If Phoenix needs spans that a CLI does not emit, the conversion lives in Collector configuration where possible and in Go only when necessary.

The agent CLIs already expose the extension points required for this boundary: Codex provides hooks and OTel configuration, while Claude Code provides hooks and detailed OTel monitoring. These are integration contracts, not functionality for this repository to recreate.

## Accepted v0 risks

- Project content can leave the container through open egress.
- While a run is live, a compromised agent can spend subscription quota through the model proxy.
- A compromised agent can use its read-only GitHub token and AWS read session for their lifetime.
- A vendor change to subscription authentication can stop a CLI until the proxy is updated; pinned versions bound this to upgrades.
- Telemetry can be lost between a native exporter and the Collector; loss at the Collector is counted, not prevented.

## Alternatives considered

| Decision | Chosen | Alternatives and why not now |
|---|---|---|
| Run identity | Container lifetime | Per launch splits one task's agents across runs. Per task label is not unique and spans replaced containers. |
| Broker authorization | One host daemon, one socket per container, authority from the socket | A shared socket with a bearer token puts a reusable secret in the container. A per-run broker process has no supervisor for detached containers. Peer credentials cannot distinguish rootless containers. |
| Model logins | Host custody with placeholder credentials and a run-socket model proxy | Real tokens in agent-home volumes expose durable account credentials and account standing to any compromised agent. Injecting a scoped token as an environment variable still places a long-lived token in the container. Per-project volumes add logins without reducing exposure. API-key billing is out of scope. |
| Egress | Open in v0, allowlisted proxy in v0.5 | Restricting from day one blocks dependency installs before the allowlist is learned from evidence. A logging-only proxy is incomplete because tools can ignore proxy settings. |
| GitHub writes | Host gateway only | Rulesets are unavailable for private repositories on the Free tier and are deferred until proven needed. A write token in the container would also permit arbitrary pull-request and comment writes. |
| Views | Phoenix in v0, Langfuse in v0.5 | Both in v0 adds a six-service Langfuse deployment to `ai up` and a second span mapping before the first is stable. |

## v0.5

v0.5 follows once v0 passes its proofs and adds only what v0 evidence shows is missing:

- Langfuse as a second Collector destination, after the span mapping is stable against Phoenix.
- A SQLite index rebuilt from run directories, for search and correlation across runs.
- Restricted egress: an internal Podman network whose only route out is a host proxy with a profile allowlist and a per-run destination log.
- Wrapper rewrites and project-declared safe alternatives, as explicit argv arrays in host policy, if refusal evidence shows agents repeatedly need them.

## v0 exclusions

The following are intentionally not part of the first foundation:

- Langfuse, the SQLite index, restricted egress, and wrapper rewrites, which are v0.5.
- Goal or specification workflow.
- Cross-model or adversarial review orchestration.
- Scheduler, rate-limit recovery, or unattended continuation.
- Mobile approvals or Telegram integration.
- Meta-optimizer, retrospectives, findings experiments, or automatic skill updates.
- AWS write operations.
- Provider-side push rules such as GitHub rulesets or branch protection.
- Hard token or cost budgets.
- Per-agent identity.
- Gemini support.
- Private checkout and apply workflow.
- Worktree policy or other prescribed development methodology.
- Devcontainer overlay generation, Docker compatibility, or general environment orchestration.
- Generic provider, plugin, or policy frameworks without a proven second implementation.

Evidence uses an open `ai.*` attribute namespace, so later layers add attributes such as goal, skill, or review identifiers without a schema redesign.

## Provisioned v1 direction

These layers are built on measured usage and remain outside the broker and native agent loops. Each starts only when run evidence shows its trigger.

1. A small shared source of skills and project guidance synchronized into native Claude and Codex extension points, including lightweight goal shaping, project setup, quality checks, and review procedures. Trigger: the same guidance is maintained separately for each CLI.
2. Lightweight iterative goals based on a short JTBD statement, explicit constraints, the next testable slice, and recorded goal pulses rather than an exhaustive initial specification. Trigger: runs abandoned or redirected because the goal was unclear.
3. Cross-agent orchestration that can assign a primary agent and use another as reviewer, advisor, or rate-limit fallback while keeping the task and evidence continuous. Trigger: measured rate-limit stalls or repeated manual cross-agent review.
4. Review depth selected by risk and prior attempts, with a hard stop after three unsuccessful review rounds. At that point an advanced model performs an architecture-level meta-review and sends the human a concise informational report; another rejected round or an inconclusive meta-review stops autonomous work and requests a human decision. Trigger: runs with repeated review rounds.
5. Shared limit recovery, cancellable scheduling, and concise remote human decisions that interrupt only for blocked or critical choices. Trigger: human wait time dominates run duration.
6. A meta-optimizer that combines deterministic aggregation and anomaly detection with selective AI diagnosis and human-approved skill, rule, and routing experiments. Trigger: deterministic aggregation shows recurring failure patterns across runs.

These layers consume the foundation; they do not expand the authority of the container or replace native agent loops.

## Implementation strategy

The v0 implementation starts greenfield inside this repository. The current implementation is frozen as a source of tested primitives and security lessons, not refactored into the new shape.

The new code must not import legacy orchestration, workspace, identity, launcher, devcontainer, or Langfuse-provider packages. Small proven pieces may be extracted after focused review: GitHub signing and permission-subset logic, secure atomic persistence, audit-before-action behavior, and useful native telemetry parsing.

The intended v0 is approximately 2,500 to 3,500 production Go lines across about five product packages, plus Containerfile and OTel Collector configuration. This is a design constraint, not a quota: exceeding it requires a concrete security or operability justification.

Once v0 passes its end-to-end proofs, legacy code and obsolete active documentation are removed in normal append-only commits. Repository history remains the archive; no history is rewritten.

## Proof of alignment

The v0 foundation is complete only when automated checks demonstrate all of the following:

- A new project can start Claude Code or Codex through one documented path.
- The container cannot access host GitHub or AWS durable credentials, the operator home, host agent login state, or unrelated repositories.
- A request on one run's socket cannot obtain credentials or act for another repository, branch prefix, or AWS role.
- The container holds no real model-provider credential, and a placeholder copied out of the container cannot reach the provider.
- Claude Code and Codex reach their models only through the model proxy, and model access stops when the broker is unavailable.
- A CLI upgrade is adopted only after the model-proxy compatibility check passes.
- GitHub reads work, direct writes fail at the provider, and the host gateway performs only recorded append-only writes within the run's branch prefix.
- AWS access uses the configured role, short lifetime, run source identity, and provider-enforced maximum permissions.
- Bypassing a wrapper or helper cannot increase provider authority.
- Audit evidence is durable before any credential or host action is released, and an audit write failure blocks the action.
- Native telemetry, broker audit, and container lifecycle for a run carry the same run ID and land in one run directory.
- Telemetry loss at the Collector is counted and visible in `ai logs`.
- A run remains inspectable locally when Phoenix or the network is unavailable and appears in Phoenix when it is available.
- Each run records its egress mode.
- Container replacement preserves only the agent-home volumes and declared caches, starts a new run with the same task label, and lets Claude and Codex start without re-onboarding and resume prior sessions.
- A second task container on the same checkout is refused.
- Claude Code and Codex remain native, upgradeable components rather than forked or reimplemented runtimes.

Any proposed feature that does not strengthen these proofs or build directly on their evidence is outside the current product focus.
