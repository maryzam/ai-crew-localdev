# Refactoring Plan

## Objective

Reduce complexity, duplication, domain leakage, test friction, and source comments while preserving or improving broker security, auditability, operational evidence, and CLI usability. Documentation declares the target; code, automated checks, health behavior, and measured budgets must enforce it.

## Non-Negotiable Outcomes

- Brokered credentials remain fail closed and no durable credential or signing material crosses the broker boundary.
- Every accepted governance action produces durable audit evidence. Queue saturation and storage failure are observable failures, never silent data loss.
- Governance configuration publication is atomic, owner-only, validated before activation, and recoverable from a committed journal without production fault-injection seams.
- Provider-specific clients, signing, configuration, and payloads do not leak into broker core, CLI presentation, or unrelated domains.
- Telemetry export is controlled by one explicit allowlist and automated tests prove that sensitive values cannot cross the export boundary.
- User-facing commands retain intentional exit behavior, actionable diagnostics, and managed-session workflows; unused compatibility aliases and transitional paths are removed.
- Source code contains no explanatory comments or lint suppressions. Only executable compiler or repository-tool directives remain.
- Prose is not hard wrapped. Each paragraph and list item occupies one source line.

## Delivery Sequence

### 1. Make Governance Evidence Reliable

Status: Complete in the Phase 1 change.

- Replace fire-and-forget audit logging with an interface that reports persistence or saturation failure to the broker decision path.
- Define and enforce deterministic behavior for audit queue saturation, write failure, shutdown, and concurrent logging.
- Add saturation, storage-failure, shutdown, and accepted-request-to-audit-record tests.
- Make identities, policy, session metadata, PID state, and other governance files use one secure atomic-write primitive where appropriate.
- Prove through public persistence operations that concurrent readers observe one complete generation and committed journals recover without mixed configuration.

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

- Extract reusable setup and readiness workflows with explicit inputs and results; keep `up` sequencing at the command boundary until a second adapter creates a real application boundary.
- Keep Cobra flags, prompting, formatting, and exit mapping in CLI adapters.
- Inject narrow filesystem, process, broker, provider-discovery, and clock ports only where a real external boundary exists.
- Replace mutable package-global test seams with constructed dependencies.
- Preserve command text and behavior through a small set of CLI acceptance tests while moving branch coverage to use-case tests.

Acceptance evidence: thin command handlers, no workflow step narration, isolated use cases, no mutable global seams, and unchanged acceptance fixtures for supported user flows.

### 4. Consolidate Telemetry Policy and Transport

Status: Complete in the Phase 4 change.

- Separate run lifecycle state, local persistence, OTLP projection, native ingestion, and transport delivery.
- Replace nested untyped OTLP maps with the smallest practical typed wire structures.
- Make one field-policy registry authoritative for local retention, export eligibility, sensitivity, cardinality, and value budgets.
- Replace nil recorder behavior with a real Null Object or explicit recorder interface.
- Keep queue limits, payload limits, timeouts, terminal-event preservation, and one-shot warnings executable; require a transport benchmark before changing delivery behavior.

Acceptance evidence: privacy tests cover every export path, no sensitive field is exportable without an explicit policy entry, lifecycle tests do not depend on transport internals, saturation and export failures are visible, and local sink throughput has a repeatable benchmark.

### 5. Remove Redundant Tests, Comments, and Compatibility Debris

Status: Complete in the Phase 5 change.

- Delete explanatory source comments after names and boundaries carry the intent.
- Replace the incremental inline-comment check with a repository-wide zero-comment check that permits only executable directives.
- Merge duplicate memfd, environment-scrubbing, managed-session, and session-invariant tests while retaining each distinct security failure mode.
- Remove standard-library JSON round-trip tests and retain wire-shape, custom encoding, and end-to-end contract tests.
- Remove production helpers used only by tests, including exported verification helpers.
- Record test runtime and failure-detection coverage before and after pruning; lower LOC alone is not acceptance evidence.

Acceptance evidence: fewer tests and fixtures with no lost security or UX failure mode, no explanatory source comments, no lint suppressions, and faster or equal focused verification time.

## Iteration Protocol

Each iteration delivers one reviewable slice with no unrelated cleanup. Before implementation, record the current behavior, focused checks, relevant benchmark, and failure policy. Do not introduce transitional compatibility paths without an active consumer and an explicit removal condition. Before handoff, update this document with completed work, evidence, unresolved risks, and the next smallest slice.

Every iteration handoff records:

- Current phase and exact slice.
- User-visible and security behavior before and after the slice.
- Decisions encoded in types, policy, checks, and metrics.
- Files and boundaries changed.
- Focused tests, race checks, static checks, and benchmark results.
- Known risks, deferred work, and whether any temporary compatibility path remains.
- Clean worktree status, commit, branch, and the next executable step.

## Current Handoff

- State: all five phases and the additional simplification pass are implemented; full post-commit verification and remote publication are next.
- Completed: relay-only up orchestration and CLI readiness adapters are gone; workflow dependencies are functions or concrete collaborators at actual boundaries; governance persistence exposes one locked snapshot and one atomic publication path; production fault-injection stages and synthetic boundary tests are gone; telemetry uses typed policy-filtered DTOs without unused runtime counters; the source checker uses Go scanning and the repository's actual comment syntaxes; unused compatibility aliases and nil paths are removed.
- Behavior: broker and persistence security contracts remain fail closed; committed governance journals recover through the public load path; concurrent readers see one complete generation; telemetry retains authenticated native ingress, sensitive-field suppression, bounded queues and payloads, terminal-event preservation, export warnings, and local persistence; setup and readiness retain actionable interactive, non-interactive, text, and JSON behavior without a redundant blocking dimension.
- Decisions encoded: comments cannot carry security or lifecycle claims; the source gate permits only executable directives; correlation validation belongs to the correlation domain; transport saturation and export failure are operator-visible behavior; performance evidence comes from boundary benchmarks rather than runtime counters with no consumer; no transitional compatibility path remains.
- Verification so far: repository compilation, vet, source-comment, dependency, diff, focused persistence tests, focused telemetry tests, and focused workflow and persistence race tests pass. `BenchmarkLocalSinkWrite` records 11,168 ns/op, 3,033 B/op, and 16 allocations on the current host. Against `main`, production Go is 11,435 lines, down 251; test Go is 12,770 lines, down 1,121; named tests and benchmarks are 341; the full working diff is 6,486 additions and 7,941 deletions, net minus 1,455.
- Remaining risk: native log ingestion intentionally accepts bounded provider JSON before extracting known usage fields; the full post-commit verification set and CI documentation linters remain to run.
- Next slice: commit the simplification pass, run `make verify` and the full suite, update this evidence, and push `refactor/enforceable-architecture`.
