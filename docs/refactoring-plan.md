# Refactoring Plan

## Objective

Reduce complexity, duplication, domain leakage, test friction, and source comments while preserving or improving broker security, auditability, operational evidence, and CLI usability. Documentation declares the target; code, automated checks, health behavior, and measured budgets must enforce it.

## Non-Negotiable Outcomes

- Brokered credentials remain fail closed and no durable credential or signing material crosses the broker boundary.
- Every accepted governance action produces durable audit evidence. Queue saturation and storage failure are observable failures, never silent data loss.
- Governance configuration publication is atomic, owner-only, validated before activation, and recoverable under fault injection at every persistence boundary.
- Provider-specific clients, signing, configuration, and payloads do not leak into broker core, CLI presentation, or unrelated domains.
- Telemetry export is controlled by one explicit allowlist and automated tests prove that sensitive values cannot cross the export boundary.
- Existing user-facing commands, exit behavior, actionable diagnostics, and managed-session workflows remain compatible unless a separately approved UX change is declared and measured.
- Source code contains no explanatory comments or lint suppressions. Only executable compiler or repository-tool directives remain.
- Prose is not hard wrapped. Each paragraph and list item occupies one source line.

## Delivery Sequence

### 1. Make Governance Evidence Reliable

Status: Complete in the Phase 1 change.

- Replace fire-and-forget audit logging with an interface that reports persistence or saturation failure to the broker decision path.
- Define and enforce deterministic behavior for audit queue saturation, write failure, shutdown, and concurrent logging.
- Add saturation, storage-failure, shutdown, and accepted-request-to-audit-record tests.
- Make identities, policy, session metadata, PID state, and other governance files use one secure atomic-write primitive where appropriate.
- Add fault-injection tests proving that readers observe either the previous complete state or the next complete state, never truncation or a mixed configuration generation.

Acceptance evidence: zero lost accepted audit events, explicit unhealthy or failed-request behavior when durability cannot be guaranteed, owner-only file modes, symlink defenses, and passing broker safety tests under race detection.

### 2. Establish Explicit Domain Boundaries

Status: Complete in the Phase 2 change.

- Extract broker socket DTOs, method names, error codes, duration encoding, and resource identifiers into a transport-contract package with no server implementation dependencies.
- Keep authorization, session lifecycle, rate limiting, cache decisions, and audit decisions in broker core.
- Move GitHub HTTP, JWT signing, provider configuration, credential payloads, and onboarding types into the GitHub provider boundary.
- Move managed-session environment and bind-FD loading into one shared session-auth package used by both command wrappers.
- Add an automated dependency-boundary check so forbidden imports fail deterministically.

Acceptance evidence: broker clients depend only on the transport contract, provider adapters depend on broker ports rather than broker concrete integrations, composition occurs only at executable roots, and existing wire-contract tests remain unchanged.

### 3. Separate CLI Presentation from Application Workflows

Status: Complete in the Phase 3 change.

- Convert `up`, `setup`, and `doctor` into small application use cases with explicit inputs and results.
- Keep Cobra flags, prompting, formatting, and exit mapping in CLI adapters.
- Inject narrow filesystem, process, broker, provider-discovery, and clock ports only where a real external boundary exists.
- Replace mutable package-global test seams with constructed dependencies.
- Preserve command text and behavior through a small set of CLI acceptance tests while moving branch coverage to use-case tests.

Acceptance evidence: thin command handlers, no workflow step narration, isolated use cases, no mutable global seams, and unchanged acceptance fixtures for supported user flows.

### 4. Consolidate Telemetry Policy and Transport

- Separate run lifecycle state, local persistence, OTLP projection, native ingestion, and transport delivery.
- Replace nested untyped OTLP maps with the smallest practical typed wire structures.
- Make one field-policy registry authoritative for local retention, export eligibility, sensitivity, cardinality, and value budgets.
- Replace nil recorder behavior with a real Null Object or explicit recorder interface.
- Measure payload size, queue saturation, export latency, local write latency, and dropped or rejected telemetry before changing buffering or delivery behavior.

Acceptance evidence: privacy tests cover every export path, no sensitive field is exportable without an explicit policy entry, lifecycle tests do not depend on transport internals, and measured budgets are checked automatically.

### 5. Remove Redundant Tests, Comments, and Compatibility Debris

- Delete explanatory source comments after names and boundaries carry the intent.
- Replace the incremental inline-comment check with a repository-wide zero-comment check that permits only executable directives.
- Merge duplicate memfd, environment-scrubbing, managed-session, and session-invariant tests while retaining each distinct security failure mode.
- Remove standard-library JSON round-trip tests and retain wire-shape, custom encoding, compatibility, and end-to-end contract tests.
- Remove production helpers used only by tests, including exported verification helpers.
- Record test runtime and failure-detection coverage before and after pruning; lower LOC alone is not acceptance evidence.

Acceptance evidence: fewer tests and fixtures with no lost security or UX failure mode, no explanatory source comments, no lint suppressions, and faster or equal focused verification time.

## Iteration Protocol

Each iteration delivers one reviewable slice with no unrelated cleanup. Before implementation, record the current behavior, focused checks, relevant benchmark, and failure policy. During implementation, preserve compatibility unless the slice explicitly changes it. Before handoff, update this document with completed work, evidence, unresolved risks, and the next smallest slice.

Every iteration handoff records:

- Current phase and exact slice.
- User-visible and security behavior before and after the slice.
- Decisions encoded in types, policy, checks, and metrics.
- Files and boundaries changed.
- Focused tests, race checks, static checks, and benchmark results.
- Known risks, deferred work, and whether any temporary compatibility path remains.
- Clean worktree status, commit, branch, and the next executable step.

## Current Handoff

- State: Phase 3 implementation is complete and Phase 4 is next.
- Completed: `internal/onboarding` owns signing, GitHub discovery, repository selection validation, governance configuration assembly, validation, and publication; `internal/readiness` owns doctor checks behind cohesive host, binary, broker, repository, governance, and policy ports; `internal/application/up` owns fail-stop startup sequencing; `internal/uphost` owns workspace preparation, context-aware broker supervision, observability startup, and container process execution; `internal/devcontainer` owns runtime parsing, root discovery, overlay generation, and exact process arguments; Cobra adapters retain flags, prompts, progress rendering, and exit mapping; the executable root constructs provider services and fresh setup, doctor, policy-validation, and up commands.
- Behavior: setup prompts and output, doctor text and JSON, up ordering and generated container commands, provider validation, broker failure behavior, and project/generic devcontainer flows remain compatible; Langfuse environment parsing is now side-effect free and the up adapter explicitly publishes the derived observability resource.
- Decisions encoded: application, host, and devcontainer packages do not import Cobra or CLI adapters; command options and external dependencies are constructed rather than stored in mutable production globals; setup and doctor branch coverage moved into focused use-case tests; the up use case has deterministic ordering and fail-stop tests; provider services flow from `cmd/ai-agent` through `NewRoot` without process-global configuration; overlay publication uses the secure owner-only atomic writer; command cancellation propagates through signal-aware root context and context-aware host runners.
- Verification: the uncached full repository suite passes in 9.3 seconds with CLI at 0.457 seconds, devcontainer at 0.102 seconds, readiness at 0.004 seconds, uphost at 0.003 seconds, and onboarding and up orchestration at 0.002 seconds each; uncached focused race tests pass for all six Phase 3 packages; vet, integration-tag compilation, dependency checks, semantic checks, lint, and diff checks pass.
- Remaining Phase 3 risk: CLI process adapters still own environment mutation, signal lifecycle, subprocess stdio, and platform-specific installation commands by design; Phase 4 must not pull those concerns into telemetry policy or transport.
- Next slice: measure current telemetry delivery behavior, introduce typed OTLP wire DTOs, consolidate export policy into one authoritative registry, and replace nullable recorders with a non-nil disabled implementation.
