# ADR 0014: Isolated Run Home

## Status

Accepted

## Context

The gap analysis required a decision on the enforcement boundary for adversarial or confused agents: an isolated per-run home, a general network egress policy, removal of real tools, or an explicitly documented trust limit. Managed runs scrubbed ambient credentials from the environment and interposed brokered tooling, but the agent process inherited the operator's `HOME`, so stored personal credentials — `~/.config/gh`, `~/.ssh`, `~/.gitconfig`, arbitrary dotfiles — stayed reachable through every tool that derives paths from the home directory.

## Decision

Managed runs execute the agent with an ephemeral `HOME` by default. The launcher creates a per-run temporary directory, projects an explicit registry of agent login state (`.claude`, `.claude.json`, `.codex`, `.agents`) as a detached snapshot, rewrites `HOME`, and drops the `XDG_CONFIG_HOME`, `XDG_DATA_HOME`, `XDG_STATE_HOME`, and `XDG_CACHE_HOME` overrides so they re-derive from the ephemeral home. Codex install and scratch subtrees under `.codex/packages` and `.codex/tmp` are preserved in the real home but not projected into the run home. Directory state is copied into the run home rather than symlinked, so `..` traversal through projected directories cannot resolve back into the real home; source symlinks are not projected into the run home, run-created symlinks are rejected on commit, and untouched source symlinks are preserved in durable state. File state is copied into the run home and committed back to the real home atomically if changed, so direct writes, read-modify-write saves, atomic renames, token refreshes, and logout deletion use normal CLI file semantics. Directory state is committed back through a staged replacement when changed, preserving untouched source symlinks and excluded Codex install/cache entries and failing closed for top-level kind changes or unsupported entries. The commit runs before the launcher reports a clean result and on agent failure, verification failure, and interruption; commit or cleanup failures are surfaced with the run error and recorded in telemetry. Everything an agent writes under `HOME` outside the projection disappears with the run. The verify contracts inherit the same environment. Isolation is on by default on the supported path and opt-out per run.

This is a confused-agent boundary, not an adversarial one. The agent process runs as the operator's UID and can still open the real home through an absolute path, and no general network egress policy exists. A host crash or `SIGKILL` before the launcher commits file state can still lose a token rotation that only existed in the run copy. Bind mounts are the preferred future mechanism when the runtime owns a per-run mount namespace, but the current native launcher and hardened generic devcontainer do not have that boundary. Those limits stay stated rather than claimed: deeper containment (per-run user namespaces, egress allowlists, real-tool removal) remains future runtime work, consistent with the standing decision that the managed runtime is an execution environment rather than the primary security boundary.

## Consequences

Path-derived access to stored personal credentials is closed on the supported path: `gh`, `git`, and SSH tooling invoked by the agent see an empty home apart from projected agent login state, complementing environment scrubbing and fail-closed git configuration. The invariant is executable: planted personal credentials are unreachable directly and through `..` traversal under projected directories while agent login state remains readable, writable, and durable across the run, including `mkdir`-based first logins into an empty real home, atomic file replacement, directory commit-back, failed-run token rotation, unchanged no-op commits, symlink rejection for run-written projected state, and drift warning.

Agents that legitimately need home-relative state beyond the projection will see it vanish after the run; the projection registry grows by decision, not by default. Operators who need the old behavior disable isolation per run and accept the stated exposure.
