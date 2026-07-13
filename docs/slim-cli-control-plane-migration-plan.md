# Slim CLI and Control Plane Migration Plan

This document tracks the migration from `origin/main` at `cb30a781c1e5830c7b03980e35967f76bf3358e9` to the north-star slim CLI and control plane architecture described in `docs/current-north-star-architecture.md`. It is a handover artifact for incremental PRs, not a second product roadmap.

Backward compatibility is not a constraint for this migration. Clean sequencing is a constraint: every PR must keep the supported managed-run path executable, prove its boundary claim with a focused automated check, and update this tracker before handoff.

## Target Shape

- `ai-agent` CLI owns flags, prompts, output formatting, JSON formatting, and exit-code mapping only.
- The control plane resolves host governance, project manifest intent, agent capability, provider capability, the managed devcontainer runtime boundary, quality contracts, telemetry sinks, token and cost budgets, retry policy, cleanup policy, and correlation IDs into one immutable `RunPlan`.
- The executor runs a `RunPlan` mechanically: create broker session, prepare the runtime boundary, start event subscribers, launch the agent, run quality contracts, finalize state, revoke the session, and emit final events.
- The broker remains the only durable secret boundary and continues to revalidate policy, mint scoped credentials, enforce rate limits, and record durable audit intent and result evidence.
- Provider and agent behavior is declared through compiled capability registries, not duplicated in CLI, launcher, broker composition roots, telemetry helpers, or tests.
- Events are the operational source of truth for run history, live budgets, export projection, adaptive findings, and CLI or cockpit views.

## Current Baseline

- The multi-call binary already dispatches CLI, broker, `gh`, and git credential helper entrypoints from `cmd/ai-agent/main.go`.
- `internal/control` owns managed-run planning for the current `ai-agent run` path: manifest loading, agent allowlist/tool binding, helper and socket resolution, repository resolution, observability resource selection, interception projection, home projection, quality contracts, retry, and run correlation are represented in `plan.RunPlan`.
- `internal/runtime/launcher` is a devcontainer-only plan executor. It validates the planned snapshot before side effects, consumes planned broker resources, interception profiles, command wrappers, quality contracts, retry policy, cleanup, and home projection rules, and no longer resolves repositories, providers, or verification contracts at execution time.
- Provider construction, resource grammar, telemetry egress eligibility, setup/readiness declarations, and interception projection are being consolidated into `internal/providers/capabilities`, with executable provider construction still projected through `internal/providers/catalog`.
- There is no agent registry, and operational commands still need to converge on the same capability and governance resolver used by managed runs.

## Migration Rules

- A managed run must reach execution only through `resolve -> plan -> execute -> emit events -> render views`.
- The planned execution boundary is devcontainer-only. Native host execution is not a compatibility requirement and should be removed as the planner/executor boundary lands.
- Planning failures must occur before a broker session is created, a credential is minted, a workspace is changed, a bind token is created, or an agent process starts.
- The executor may branch only on fields already present in the plan or on observed process outcomes; it must not discover new providers, agents, resources, credentials, contracts, budgets, or egress destinations.
- Runtime and broker enforcement must fail closed on governance, audit, credential, interception, and budget paths. Telemetry export may fail open only after local run evidence is retained.
- Run event projections must fail explicitly on unsupported event schemas and must not present partial or empty history as success when durable JSONL evidence needs a migration-capable reader.
- Each PR must include a focused check that would fail if the moved responsibility leaked back across the boundary.
- Dependency rules should move with the code. When a package boundary becomes real, `scripts/check-dependencies.sh` or an equivalent invariant test must enforce it in the same PR.

## Progress Tracker

| Step | Status | PR scope | Exit evidence |
|---|---|---|---|
| 0 | Done | Create this migration tracker from the latest `origin/main` state. | Docs checks listed in the PR. |
| 1 | Done | Add a first-class devcontainer-only `RunPlan` contract and package boundary. | `go test ./internal/control/plan` and `make dependency-check` prove plan validation, snapshot isolation, and the dependency boundary. |
| 2 | Done in PR #94 | Introduce the control-plane planner behind `ai-agent run` and deliberately reject native host execution. | `go test ./internal/control/... ./internal/cli`, `go test ./internal/platform/binresolve ./internal/control/... ./internal/cli ./internal/runtime/launcher`, `make readiness`, `make verify-code`, and `make dependency-check` proved manifest, identity, command/tool binding, repo, socket, helper, observability resource, quality contract, retry, home, telemetry, and devcontainer boundary fields are resolved before executor handoff. |
| 3 | Done in PR #95 | Convert `internal/runtime/launcher` into a devcontainer plan executor and remove duplicated resolution from it. | Executor tests prove `launcher.Launch` consumes `plan.RunPlan`, uses planned broker resources, run ID, interception, scrub policy, quality contracts, retry, cleanup, and home policy, rejects unsupported non-devcontainer and invalid-plan entries before broker access, and no longer resolves repositories, resources, provider profiles, or verification contracts at execution time. |
| 4 | Done in PR #96 | Consolidate provider capabilities into one compiled registry consumed by planner, broker, readiness, setup, and runtime. | Contract tests prove each registered provider has a broker provider, resource grammar or validator, interception profile, readiness/setup declarations if applicable, telemetry egress capability if applicable, and no duplicate provider list exists. |
| 5 | Done in PR #98 | Add first-class agent adapters for Claude and Codex. | PR #97 introduced the compiled agent capability registry and wired executable matching, native telemetry support, login-state projection, auth probes, and default guidance. PR #98 moved managed-run model attribution through the control-plane plan boundary and centralized model attribution primitives so production telemetry consumes planned agent metadata without rediscovering it in the telemetry package. |
| 6 | Done in PR #99 | Move quality contract resolution and retry policy fully into the plan. | Contract failure policy, evidence retention budget, tail budget, and retry budget are explicit plan fields consumed by the executor without using quality defaults or storing derived retry attempts. |
| 7 | Done through PR #101 | Move run events toward an event spine and split telemetry concerns along model, local store, native relay, export projection, budget subscriber, and adaptive ledger boundaries. | PR #100 introduced `internal/platform/runevents` as the event DTO, JSONL store, and history projection owner. PR #101 moved native usage aggregation into `runevents` so live budget enforcement can subscribe to provider-reported usage without depending on telemetry relay internals. |
| 8 | In progress on `refactor/live-budget-enforcement` | Implement live token budget enforcement through the native usage relay. | This slice adds CLI-sourced token budget planning, explicit native measurement sources, native usage relay subscription, durable threshold events, and launcher-owned process stop behavior for planned `native_otel` budgets. |
| 9 | Pending | Slim CLI imports and presentation paths after planning and execution boundaries are enforceable. | Dependency checks prove CLI packages cannot import provider implementations, runtime internals, broker core, or config model packages except through the control-plane API and explicit presentation-only exceptions. |
| 10 | Pending | Reconcile `ai-agent up`, readiness, setup, and doctor onto the shared governance resolver and capability registries. | Readiness and setup tests prove they use the same resolved identities, policy, manifest, environment contract, provider registry, and agent registry as managed runs. |

## PR Slices

### PR 1: RunPlan Contract

Create `internal/control/plan` or equivalent with typed plan structures for repository, command, agent, provider resources, broker session request, managed devcontainer boundary, environment changes, interception plan, home projection policy, telemetry sinks, budgets, quality contracts, retry policy, cleanup policy, and correlation IDs. The first contract is intentionally devcontainer-only; native execution is not represented as a plan mode. The package should contain validation helpers but no filesystem, process, broker client, provider SDK, Cobra, or runtime side effects.

Checks: `go test ./internal/control/...`, `scripts/check-dependencies.sh`, and a targeted validation test proving incomplete security fields are rejected.

### PR 2: Planner Shell

Add `internal/control` as the run planner. Move manifest loading, host identity lookup, agent allowlist enforcement, configured-tool binding, model attribution defaulting, repo resolution, task-ref validation, broker socket resolution, helper resolution, observability resource validation, verification contract selection, retry budget validation, home projection selection, devcontainer boundary selection, and run ID creation from CLI and launcher into the planner. `ai-agent run` should build a presentation-level request from flags and call the planner.

Checks: planner table tests for every fail-closed input, including invalid manifest, disallowed agent, mismatched command/tool, SSH remote, malformed task ref, invalid observability resource, missing credential helper, missing devcontainer boundary, native execution request, and out-of-range retry budget. Tests must assert no broker client is constructed for planner failures.

### PR 3: Plan Executor

Replace `launcher.Options` with `RunPlan` or a thin executor-only projection. Remove repo/resource/contract/budget/profile discovery from `internal/runtime/launcher`; it should consume resolved plan fields and observe process outcomes only. Remove native host process launch as a supported managed-run path instead of preserving it as a runtime mode. Keep devcontainer process supervision, bind FD creation, wrapper setup, home projection, native telemetry relay startup, verification command execution, cleanup, and session revocation in runtime packages until later splits have a better owner.

Checks: executor tests with fake broker and fake process runner proving the side-effect order is session create, bind setup, devcontainer setup, relay setup, agent, quality, cleanup, revoke, final event. Tests must prove plan validation failure does not create a session and no native execution entrypoint remains reachable.

### PR 4: Capability Registry

Replace the split provider composition roots with one compiled capability registry. Each provider entry should carry broker provider construction, validation/resource grammar, interception profile, setup/readiness hooks, and telemetry egress declaration where applicable. Broker startup, policy validation, setup, readiness, planner, and runtime wrapper selection should consume that registry or typed projections from it.

Checks: registry contract tests proving GitHub and Langfuse are complete entries; dependency checks proving runtime and CLI consume contracts or registry projections rather than provider implementations; policy validation tests proving provider resources still fail closed.

### PR 5: Agent Registry

Introduce typed agent adapters for Claude and Codex. Move command matching from CLI, model extraction from telemetry helpers, native telemetry support from launcher, login-state projection rules from runtime home-state logic, auth status probes from app auth, and guidance asset selection from agent defaults into adapter-owned declarations where practical. Do this in more than one PR if the first adapter shape is too large.

Checks: adapter tests for Claude and Codex command names, configured tool aliases, model extraction, telemetry wrapping decisions, login-state persistence declarations, and auth probes. Add dependency checks once consumers no longer need direct agent-specific string lists.

### PR 6: Quality and Retry Plan

Make quality contracts a planned execution graph instead of launcher options. Represent contract command, working directory, retry policy, evidence retention budget, tail budget, timeout if introduced, and failure policy in the plan. The executor should run planned contracts through `internal/quality` without deriving retry behavior from manifest or flags.

Checks: tests proving manifest contracts and `--verify-cmd` compile to the same planned contract type, `retry: never` prevents relaunch, evidence retention failures are recorded without losing the primary failure, and retry counts are explicit budgets.

### PR 7: Event Spine

Split `internal/platform/telemetry` only where a new boundary has an immediate consumer: event model, local event store, native relay ingestion, remote export projection, budget subscriber, run summary projection, and adaptive ledger integration. Keep local JSONL durability as the first invariant and keep remote export optional. Views such as `ai-agent runs` should render from event-derived summaries rather than special telemetry-only state.

Checks: event model tests, local store crash-truncation tests, export failure tests, run summary projection tests, and adaptive analyzer tests using bounded reports rather than raw unbounded history.

### PR 8: Live Budgets

Add token and cost budget fields to `RunPlan` with explicit warn threshold, hard stop threshold, measurement source, and stop policy. Use native relay usage events as the input. On stop, the executor should apply the planned deterministic failure policy and emit enough event evidence for `runs show` and `runs analyze` to explain the outcome.

Checks: fake native relay tests proving warn fires once, stop terminates the agent deterministically, local events contain budget evidence, remote export failure does not suppress budget enforcement, and runs without live usage report `unknown` rather than pretending enforcement happened.

### PR 9: Slim CLI

After planner and executor boundaries are stable, remove leftover CLI-owned domain helpers. CLI code should translate flags into request structs, call control-plane APIs, render errors and summaries, and map exit codes. Delete compatibility shims that only exist to preserve old internal call paths.

Checks: dependency check that CLI imports only control-plane APIs, presentation support, Cobra, and narrow app services still intentionally owned by CLI. Add golden output tests for human and JSON views if any output changes.

### PR 10: Shared Governance Resolver for Operational Commands

Move `up`, readiness, setup, doctor, and policy validation onto the same governance resolver and capability registries used by the run planner. This closes the current risk that setup/readiness accept a configuration shape that the planner or broker later rejects differently.

Checks: focused tests showing the same invalid identity, policy, manifest, provider, and environment contract cases fail consistently across planner, broker startup, readiness, and setup paths.

## Handover Checklist

- Update the Progress Tracker status and add the PR number or branch name when a slice starts or finishes.
- Record the exact baseline assumption if the branch rebases on a newer `origin/main`.
- State any intentionally broken compatibility in the PR description.
- Include the focused checks run, and explain any skipped check.
- Keep each PR narrow enough that a reviewer can identify which boundary moved and which check enforces it.
- Do not close a migration step because code was moved; close it only when the old boundary violation is impossible or covered by an executable failure.
