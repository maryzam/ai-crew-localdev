# ADR 0015: Environment Contract

## Status

Accepted

## Context

Every binary parsed its own `AI_AGENT_*` environment variables inline: twenty-three read sites across ten files, with the policy-path fallback duplicated in three of them and the broker socket resolved under two different names — the daemon bound `AI_AGENT_BROKER_SOCKET` while the CLI resolved `AI_AGENT_AUTH_SOCK`, so an operator who moved the daemon's socket got clients that silently dialed the default path. The live E2E suite reproduced the same bug a third time by reimplementing the resolution with a wrong fallback. Scattered environment knowledge is the same failure class as inline provider lists: each duplication is a future asymmetry.

## Decision

`internal/platform/paths` is the single environment contract. Every `AI_AGENT_*` variable name is a named constant there, and every resolution rule that more than one component needs is a shared resolver: `BrokerListenSocketPath` for the daemon (`AI_AGENT_BROKER_SOCKET`, then the runtime-directory default) and `BrokerClientSocket` for everything that dials (`AI_AGENT_AUTH_SOCK` from the session scope first, then the daemon's `AI_AGENT_BROKER_SOCKET`, then the same default), plus unified policy, audit-log, and run-telemetry path resolvers. The client order means the nearest scope wins: a session-injected socket beats operator daemon configuration, which beats the default, and with no environment set the daemon and every client agree by construction.

The contract is deterministically enforced, not documented: `scripts/check-env-contract.sh` runs in `make verify` and fails on any `os.Getenv`/`os.LookupEnv` of an `AI_AGENT_` literal outside `internal/platform/paths` in non-test code. Shims may import the contract package; their dependency allowlist names it explicitly.

## Consequences

Environment behavior is defined once, tested once (resolver precedence tests including daemon/client symmetry), and consumed everywhere, including the E2E suites. Adding a variable means adding a constant — and a resolver if the rule is shared — inside the contract package; writing `os.Getenv("AI_AGENT_...")` anywhere else fails verification. The gate intentionally does not police environment writers (launchers composing child environments), which set many variables as data; readers define semantics, so readers are where drift becomes a bug.
