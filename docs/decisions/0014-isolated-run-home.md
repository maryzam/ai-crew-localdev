# ADR 0014: Isolated Run Home

## Status

Accepted

## Context

The gap analysis required a decision on the enforcement boundary for adversarial or confused agents: an isolated per-run home, a general network egress policy, removal of real tools, or an explicitly documented trust limit. Managed runs scrubbed ambient credentials from the environment and interposed brokered tooling, but the agent process inherited the operator's `HOME`, so stored personal credentials — `~/.config/gh`, `~/.ssh`, `~/.gitconfig`, arbitrary dotfiles — stayed reachable through every tool that derives paths from the home directory.

## Decision

Managed runs execute the agent with an ephemeral `HOME` by default. The launcher creates a per-run temporary directory, symlinks in an explicit allowlist of agent login state (`.claude`, `.claude.json`, `.codex`, `.agents`), rewrites `HOME`, and drops the `XDG_CONFIG_HOME`, `XDG_DATA_HOME`, `XDG_STATE_HOME`, and `XDG_CACHE_HOME` overrides so they re-derive from the ephemeral home. Allowlist entries are linked even when absent, so a first login inside a run writes through the dangling link and persists in the real home; everything else an agent writes under `HOME` disappears with the run. The verify contracts inherit the same environment. Isolation is on by default on the supported path and opt-out per run.

This is a confused-agent boundary, not an adversarial one. The agent process runs as the operator's UID and can still open the real home through an absolute path, and no general network egress policy exists. Those limits stay stated rather than claimed: deeper containment (per-run user namespaces, egress allowlists, real-tool removal) remains future runtime work, consistent with the standing decision that the managed runtime is an execution environment rather than the primary security boundary.

## Consequences

Path-derived access to stored personal credentials is closed on the supported path: `gh`, `git`, and SSH tooling invoked by the agent see an empty home apart from agent login state, complementing environment scrubbing and fail-closed git configuration. The invariant is executable: planted personal credentials are unreachable through `HOME` in a managed run while agent login state remains readable, writable, and durable across the run.

Agents that legitimately need home-relative state beyond the allowlist will see it vanish after the run; the allowlist grows by decision, not by default. Operators who need the old behavior disable isolation per run and accept the stated exposure.
