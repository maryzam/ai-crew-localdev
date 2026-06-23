# Current and North-Star Architecture

This document describes AI Crew localdev at a high level: what exists today,
what the north-star architecture is aiming toward, and the decisions that shape
both. It intentionally avoids implementation detail that belongs in code,
tests, ADRs, or operational docs.

## Architecture Summary

AI Crew localdev is a local control plane for AI coding agents.

Today it provides a governed credential and devcontainer foundation: agents run
inside managed sessions, GitHub credentials are brokered by a host daemon,
policy is enforced on the broker side, and executable checks protect the core
auth and container flow.

The north star is broader: a local development environment where agents work
inside project-aware workflows, quality gates are executable contracts, and a
meta-agent layer learns from runs across projects to reduce waste, failures,
and cost.

## Current Architecture

```mermaid
flowchart LR
    User[Developer] --> CLI[ai-agent CLI]
    CLI --> Up[ai-agent up]
    CLI --> Run[ai-agent run]

    Up --> Broker[Host broker]
    Up --> Devcontainer[Generic or project devcontainer]

    Run --> Session[Broker session]
    Session --> Broker
    Run --> Agent[Agent CLI process]

    Agent --> Git[git credential helper]
    Agent --> Gh[gh wrapper]
    Git --> Broker
    Gh --> Broker

    Broker --> Policy[Policy and identities]
    Broker --> GitHub[GitHub App API]
    Broker --> Audit[JSONL audit log]
```

Current responsibilities:

- `ai-agent up` prepares the local environment, starts or finds the broker, and
  launches either the generic devcontainer or a project-owned devcontainer with
  a broker/toolchain overlay.
- `ai-agent run` creates one managed session for one agent and repository,
  scrubs ambient credentials, configures fail-closed git behavior, shims `gh`
  when the wrapper is available on the toolchain path, supervises the agent
  process, and revokes the session on exit.
- The broker is the trust boundary for credential minting. It loads GitHub App
  signing keys, enforces policy, validates session binding, rate limits minting,
  and writes audit events.
- The devcontainer is an execution environment, not an authorization boundary.
  For authorization, it receives the broker runtime mount; signing material and
  PEM paths remain on the host.
- Quality enforcement is mostly repo-local today: Go tests, invariant tests,
  docs checks, readiness tests, ADR gates, semantic checks, and an ad hoc
  `ai-agent run --verify-cmd` retry loop.

## Current Auth Flow

```mermaid
sequenceDiagram
    participant User
    participant Launcher as ai-agent run
    participant Broker
    participant Agent
    participant Helper as git helper / gh wrapper
    participant GitHub

    User->>Launcher: start managed agent session
    Launcher->>Broker: create_session(agent, repo resource)
    Broker-->>Launcher: session_id + bind secret
    Launcher->>Agent: start child with session metadata + bind FD
    Agent->>Helper: git or gh operation
    Helper->>Broker: mint_credential(session_id, bind secret, resource)
    Broker->>Broker: validate UID, session, binding, policy, permissions
    Broker->>GitHub: mint GitHub App installation token
    GitHub-->>Broker: scoped token
    Broker-->>Helper: credential
    Helper-->>Agent: brokered git/gh auth
    Agent-->>Launcher: exit
    Launcher->>Broker: revoke_session
```

The important property is that the agent does not receive GitHub App private
keys or long-lived credentials. Short-lived credentials are minted on demand and
are tied to broker-side session state.

## North-Star Architecture

```mermaid
flowchart TB
    Project[Project repository] --> Manifest[ai-agent project manifest]
    Manifest --> Runtime[Project runtime orchestration]
    Manifest --> Contracts[Executable quality contracts]
    Manifest --> Policy[Agent and credential policy]

    Operator[Developer / operator] --> Cockpit[Local operator cockpit]
    Cockpit --> Runs[Run planner and session manager]

    Runs --> Agents[Agent CLIs and tools]
    Runtime --> Agents
    Policy --> Broker[Credential and secret broker]
    Agents --> Broker
    Agents --> Contracts

    Runs --> Telemetry[Run telemetry]
    Broker --> Telemetry
    Contracts --> Telemetry
    Runtime --> Telemetry

    Telemetry --> Meta[Meta-agent analysis]
    Meta --> Guidance[Workflow guidance and improvements]
    Guidance --> Manifest
    Guidance --> Cockpit
```

North-star responsibilities:

- Projects declare how agents are allowed to work: agents, credentials,
  services, secrets, caches, ports, approval points, and executable contracts.
- The runtime provisions project-specific development environments without
  baking secrets into images or relying on source-built host binaries.
- The broker generalizes beyond GitHub credentials into a governed local secret
  and credential boundary.
- The operator cockpit shows active runs, approvals, diffs, checks, traces,
  token spend, resource use, and failure patterns.
- The meta-agent layer analyzes run telemetry across projects and recommends
  concrete improvements to workflows, prompts, contracts, models, and tooling.

## Core Architecture Characteristics

| Characteristic | Current state | North-star direction |
|---|---|---|
| Governed | Broker sessions and repo-scoped GitHub policy govern the supported auth path. | Project manifests govern full workflows: credentials, contracts, services, approvals, and run modes. |
| Secure by default | Signing keys stay on the host; agents use brokered credentials on the managed path. | Clearer enforcement boundary for confused or adversarial agents, including isolated state, egress controls, and mediated secret access where needed. |
| Simple to use | `setup`, `install`, `up`, and `run` exist, but still require source-built tooling. | Clean-host install, portable toolchain delivery, persistent re-entry, and project-first commands. |
| Contract-driven | Repo tests and readiness checks protect the broker/container foundation. | Every project has executable contracts with structured outcomes and retry guidance. |
| Observable | Broker JSONL audit records auth events. Langfuse deployment is available as infrastructure. | Run-level telemetry connects auth, agent actions, verification, cost, tokens, resources, and outcomes. |
| Adaptive | Token caching reduces repeated credential minting. | A meta-agent detects repeated failures, waste, idle loops, bad model choices, and weak project contracts. |

## Key Decisions

### Explicit Decisions

- The broker API is credential-generic, not GitHub-specific. New credential
  types should be added as providers behind `mint_credential`, not as separate
  ad hoc paths. See ADR 0001.
- Session binding secrets are required for credential minting but are not
  persisted in session files. Lifecycle operations use same-UID socket
  ownership. See ADR 0002.
- The launcher supervises the agent process so it can revoke sessions on exit
  and propagate the agent exit code after cleanup.
- In the generic devcontainer and complete installed toolchain path, the real
  `gh` binary is kept off the managed command path. Plain `gh` invocations route
  through the brokered wrapper there. Host-native wrapping depends on
  `ai-agent-gh` being installed or explicitly configured.
- The broker is host-side. Containers receive the broker socket, not signing
  keys or PEM paths.

### Implicit Decisions

- The supported trust model is single-user local workstation first. The broker
  rejects other UIDs, but it does not claim protection from a fully compromised
  same-UID host process.
- The devcontainer improves packaging, repeatability, and operational safety,
  but it is not the primary security boundary.
- The supported path is fail-closed. When the broker or session binding is
  unavailable, git and `gh` should fail rather than fall back to personal auth.
- Project devcontainer support currently overlays broker access onto a
  repository-owned environment instead of replacing that environment.
- Local quality gates are treated as executable product contracts, not just CI
  hygiene.
- Observability should be built from durable run and auth events, not from
  screenshots, logs, or manual post-run notes alone.

## Current Boundaries

The current architecture can support secure brokered GitHub work for managed
sessions. It cannot yet guarantee that every possible command an agent runs is
policy-mediated. Agents can still execute arbitrary project tools and raw
network clients unless future runtime controls restrict that.

The generic devcontainer has a persistent home volume for agent CLI state.
Provisioning that state and separating personal agent CLI login state from
governed repository credentials are still open product problems.

The broker can audit credential activity, but it cannot yet explain total agent
cost, token spend, model choice, wall-clock waste, or recurring failure
patterns. Those require run-level telemetry above the broker.

Project devcontainer support proves that broker access can be injected into a
repository-owned environment. It does not yet provide portable toolchain
installation, broker-mediated secrets, cache declarations, or full service
policy.

## Decision Pressure Points

These are the architecture decisions that should be resolved before major new
features are added:

1. Enforcement boundary: accidental misuse only, or stronger containment for
   intentionally bypassing agents?
2. Distribution shape: release artifacts, package repository, published image,
   devcontainer Feature, or a combination?
3. Project manifest shape: which project workflow concerns belong in
   `ai-agent` instead of the repository's own devcontainer?
4. Telemetry identity: what is the stable run ID that connects broker events,
   agent actions, contract results, token spend, and resource use?
5. Meta-agent authority: should the meta-agent only recommend changes, or can
   it open PRs and modify project manifests under policy?

## Design Rule

Keep the broker small, strict, and auditable. Put project workflow intelligence
above it, not inside it. The broker should answer "may this session receive this
credential?" The workflow layer should answer "what should the agent do next,
how should quality be proven, and what should improve next time?"
