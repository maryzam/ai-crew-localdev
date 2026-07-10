# ADR 0016: Adaptive Findings Ledger

## Status

Accepted

## Context

`ai-agent runs analyze` re-derived and re-printed the same advice on every invocation. Nothing recorded whether a recommendation had been seen, accepted, or dismissed, and an accepted recommendation had no baseline to measure against, so the advisory loop never closed: the analyzer could not report whether following its advice changed anything.

## Decision

Findings persist in a durable ledger at the config directory, written atomically through the same owner-only temp-fsync-rename path as governance files. Each entry is keyed by a stable fingerprint of the analyzer's full finding scope — the grouping key the analyzer already uses to distinguish a recommendation, so two findings that share a kind and repository but differ in failure signature or per-agent usage scope never collapse into one entry — and carries first-seen, last-seen, a status of open, accepted, or dismissed, and, for accepted findings, a metric snapshot of the evidence at acceptance time. The scope is a referent the analyzer emits with each finding, not a label the ledger re-derives. `runs analyze` syncs the ledger on every invocation and annotates each recommendation with its fingerprint and status; accepted findings get an outcome section comparing the acceptance snapshot with current evidence, or reporting that the finding is no longer flagged. Outcome comparison and the ledger both use the analyzer's full pre-truncation finding set, so a finding pushed past the display limit is still tracked and measured rather than misreported as resolved. `--json` emits the same report enriched with each finding's fingerprint and status and the accepted-outcome deltas. `ai-agent runs findings` lists tracked findings, and `accept`, `dismiss`, and `reopen` change status by unique fingerprint prefix; `accept` honors the same analysis window and threshold flags as `analyze` so it snapshots the evidence the user reviewed, and requires the finding to be present in that window so the delta always has a real baseline.

The analyzer remains a pure function of retained history; the ledger lives beside it and is composed by the CLI. Every load-modify-write runs under an owner-only lock, like governance state, so concurrent commands cannot silently overwrite an accepted status or snapshot. The ledger has an explicit entry budget with deterministic pruning that preferentially retains accepted findings and enforces a hard cap so the file stays loadable, records how many entries it removed, and refuses to write a ledger that would exceed its size bound. A corrupt or newer-schema ledger fails the command rather than being clobbered.

## Consequences

Recommendations now have memory: repeated advice is visibly tracked rather than re-announced as new, dismissals stay recorded, and acceptance produces a measurable before/after comparison from the same deterministic evidence the finding was based on. This is the approval-controlled proposal tracking the capability map required before any automated project change.

Run-level live token budgets — the enforcement half of the adaptive milestone — remain future work, deferred until after the planned control-plane refactoring. Outcome deltas compare evidence within analyzer windows, not causal attribution; measuring recommendation efficacy across resource metrics and dashboards stays with that later work.
