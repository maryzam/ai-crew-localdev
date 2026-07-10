# ADR 0016: Adaptive Findings Ledger

## Status

Accepted

## Context

`ai-agent runs analyze` re-derived and re-printed the same advice on every invocation. Nothing recorded whether a recommendation had been seen, accepted, or dismissed, and an accepted recommendation had no baseline to measure against, so the advisory loop never closed: the analyzer could not report whether following its advice changed anything.

## Decision

Findings persist in a durable ledger at the config directory, written atomically through the same owner-only temp-fsync-rename path as governance files. Each entry is keyed by a stable fingerprint of the finding's kind and repository — the scope that identifies a recommendation across windows while its evidence changes — and carries first-seen, last-seen, a status of open, accepted, or dismissed, and, for accepted findings, a metric snapshot of the evidence at acceptance time. `runs analyze` syncs the ledger on every invocation and annotates each recommendation with its fingerprint and status; accepted findings get an outcome section comparing the acceptance snapshot with current evidence, or reporting that the finding is no longer flagged. `ai-agent runs findings` lists tracked findings, and `accept`, `dismiss`, and `reopen` change status by unique fingerprint prefix; accepting requires the finding to be present in the current window so the delta always has a real baseline.

The analyzer remains a pure function of retained history; the ledger lives beside it and is composed by the CLI. The ledger has an explicit entry budget with deterministic pruning that never drops accepted findings and records how many entries it removed. A corrupt or newer-schema ledger fails the command rather than being clobbered.

## Consequences

Recommendations now have memory: repeated advice is visibly tracked rather than re-announced as new, dismissals stay recorded, and acceptance produces a measurable before/after comparison from the same deterministic evidence the finding was based on. This is the approval-controlled proposal tracking the capability map required before any automated project change.

Run-level live token budgets — the enforcement half of the adaptive milestone — remain future work, deferred until after the planned control-plane refactoring. Outcome deltas compare evidence within analyzer windows, not causal attribution; measuring recommendation efficacy across resource metrics and dashboards stays with that later work.
