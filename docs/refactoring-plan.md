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

- Extract broker socket DTOs, method names, error codes, duration encoding, and resource identifiers into a transport-contract package with no server implementation dependencies.
- Keep authorization, session lifecycle, rate limiting, cache decisions, and audit decisions in broker core.
- Move GitHub HTTP, JWT signing, provider configuration, credential payloads, and onboarding types into the GitHub provider boundary.
- Move managed-session environment and bind-FD loading into one shared session-auth package used by both command wrappers.
- Add an automated dependency-boundary check so forbidden imports fail deterministically.

Acceptance evidence: broker clients depend only on the transport contract, provider adapters depend on broker ports rather than broker concrete integrations, composition occurs only at executable roots, and existing wire-contract tests remain unchanged.

### 3. Separate CLI Presentation from Application Workflows

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

- State: Phase 1 implementation is complete and Phase 2 is next.
- Completed: audit writes are synchronous, durable, serialized, sticky on failure, visible through broker health, and required before successful broker responses; credential minting records durable intent before provider access; unsafe audit targets are rejected; expired sessions emit audit evidence; governance configuration uses owner-only atomic writes and a durable roll-forward journal so identities and policy recover as one generation; identity, policy, Langfuse credential, and session metadata reads reject symlinks, permissive modes, and oversized files; session IDs cannot traverse paths.
- Behavior: broker wire shapes are unchanged; audit storage failure now returns `broker_unavailable`; revoked sessions remain revoked even when audit persistence fails; interrupted configuration publication completes from its journal on the next guarded configuration access.
- Verification: baseline `go test ./...`, `go vet ./...`, and integration-tag compilation passed before the phase; focused secure-file, configuration-store, identity, policy, broker, launcher, and CLI tests pass after the phase; race tests cover the touched stateful packages; injected persistence tests cover create, permission, write, file-sync, close, rename, directory-sync, partial-generation recovery, invalid journals, and locked generation reads; deterministic broker tests enforce revoke and expiration intent ordering; the synchronous audit benchmark measured 2.738 microseconds per record for 100 writes on the local temporary filesystem.
- Remaining Phase 1 risk: the audit latency measurement is filesystem-specific and must be observed on the real configuration filesystem; an audit failure after a durable mint-intent record can suppress the final result record, but the external provider action remains attributable to the durable intent and the broker becomes unhealthy; revocation still proceeds for safety when its intent cannot be persisted, returns `broker_unavailable`, and leaves the broker unhealthy rather than reporting unaudited success.
- Next slice: extract shared managed-session authentication, then the broker transport contract and provider port, without retaining broker aliases.
