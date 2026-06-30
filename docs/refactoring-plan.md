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

Status: Complete in the Phase 4 change.

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

- State: Phase 4 implementation is complete and Phase 5 is next.
- Completed: managed OTLP encoding uses typed payload, resource, scope, span, event, status, attribute, and value DTOs; the private field registry now controls managed export and native-ingestion eligibility; native aliases resolve to canonical field identifiers; sensitive and unknown native attributes are rejected; disabled telemetry returns an inert non-nil recorder; local, managed OTLP, and native OTLP delivery share measured budgets and counters.
- Behavior: managed OTLP JSON remains byte-compatible; local history and remote-failure behavior remain compatible; disabled telemetry remains zero-I/O; native usage extraction and authenticated relay behavior remain compatible; cache-creation usage now resolves to the existing canonical `ai_agent.usage.cache_write.input_tokens` field instead of creating a second unregistered wire name.
- Decisions encoded: transport-specific correlation values can only derive from non-sensitive OTLP-approved source fields; native ingestion requires explicit `NativeInput` policy; delivery statistics record payload count, total and maximum bytes, current and maximum queue depth, saturation, dropped and rejected events, export attempts and latency, local writes and latency, and each budget violation; default budgets are observational and do not silently alter delivery.
- Verification: the full repository suite passes; focused telemetry and launcher race suites pass in 1.206 and 1.305 seconds; typed OTLP compatibility, field-policy, sensitive-value, native-ingestion, disabled-recorder, deterministic-budget, local-write, and export-measurement checks pass; vet and diff checks pass; delivery snapshot costs 24.24 ns/op with zero allocations and local JSONL writes cost 10.373 microseconds per event on the verification host.
- Remaining Phase 4 risk: native ingress still decodes untrusted OTLP JSON dynamically at the relay boundary before rebuilding a typed, policy-filtered payload; this compatibility boundary remains bounded by request size, authentication, explicit field policy, and privacy tests, and no temporary compatibility path remains.
- Next slice: establish a repository-wide executable-directive-only source gate, remove explanatory comments and lint suppressions, prune redundant synthetic tests and test-only production helpers, then compare focused verification time and retained failure-mode coverage.
