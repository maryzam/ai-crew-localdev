# ADR 0015: Environment Contract

## Status

Accepted

## Context

Every binary parsed its own `AI_AGENT_*` environment variables inline: twenty-three read sites across ten files, with the policy-path fallback duplicated in three of them and the broker socket resolved under two different names — the daemon bound `AI_AGENT_BROKER_SOCKET` while the CLI resolved `AI_AGENT_AUTH_SOCK`, so an operator who moved the daemon's socket got clients that silently dialed the default path. The live E2E suite reproduced the same bug a third time by reimplementing the resolution with a wrong fallback. Scattered environment knowledge is the same failure class as inline provider lists: each duplication is a future asymmetry.

## Decision

`internal/platform/paths` is the single environment contract. Every `AI_AGENT_*` variable name is a named constant there — readers, writers, scrub declarations, and messages all compose from the constants — and every resolution rule that more than one component needs is a shared resolver: `BrokerListenSocketPath` for the daemon and everything that manages or projects the daemon's socket (`ai-agent up` broker startup, readiness, the project devcontainer overlay), and `BrokerClientSocket` for everything that dials (`AI_AGENT_AUTH_SOCK` from the session scope first, then the daemon's `AI_AGENT_BROKER_SOCKET`, then the shared default), plus unified policy, audit-log, and run-telemetry path resolvers. The client order means the nearest scope wins, and with no environment set the daemon and every client agree by construction. Validation lives in the resolvers, not in consumers: socket paths must be absolute on both sides, the daemon fails closed before binding, and readiness reports an invalid socket environment as a failed check with remediation. The raw default is unexported so the resolvers are the only entry to the socket concern — bypassing the contract is a compile error, not a review finding.

The contract is deterministically enforced at the name level: `scripts/check-env-contract.sh` runs in `make verify` and fails on any `"AI_AGENT_` string literal in non-test Go outside `internal/platform/paths`, which also closes indirection through locally defined constants. Shims and provider contract packages may import the contract package; their dependency allowlists name it explicitly.

## Consequences

Environment behavior is defined once, tested once (resolver precedence, daemon/client symmetry, relative-path rejection, overlay propagation), and consumed everywhere, including the E2E suites. Adding a variable means adding a constant — and a resolver if the rule is shared — inside the contract package; an `AI_AGENT_` literal anywhere else fails verification, so writers and scrub lists cannot drift from the names readers resolve. Provider-scoped variable names live in the same contract even when only provider packages use them, trading a wider constant list for a single greppable source of truth.
