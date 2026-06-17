# ADR 0002: Session credential integrity hardening

**Status:** Accepted
**Date:** 2026-06-17

## Context

Managed sessions use a broker-side session ID plus a binding secret delivered to
the agent process through a sealed memfd. The binding secret protects credential
minting, but persisting that same secret in the local session JSON file made the
file a credential at rest. The original launcher also replaced itself with the
agent process, so it could not reliably revoke the broker session when the agent
exited.

The devcontainer also installed the real `gh` binary on `PATH` next to the
brokered wrapper. That let an agent bypass brokered GitHub auth by invoking the
unmanaged binary with ambient credentials.

## Decision

Keep the binding secret required for `mint_credential`, but stop using it for
`session_status` and `revoke_session`. Those lifecycle operations are authorized
by the Unix socket peer UID recorded when the session is created. The bind secret
therefore remains only in broker memory and the inherited sealed memfd, and the
session JSON file contains only non-secret management metadata.

Run the agent as a supervised child instead of replacing the launcher process.
The launcher explicitly passes the sealed memfd to the child as fd 3, sets
`AI_AGENT_SESSION_BIND_FD=3`, forwards termination signals, and revokes the
session after the agent exits. Agent exit codes are propagated back to callers
after cleanup.

Move the real `gh` binary off `PATH` in the devcontainer image and route `gh`
through `ai-agent-gh`. Scrub ambient GitHub token variables before launching the
agent and before executing the real `gh` binary.

## Consequences

**Gains:**
- Session files are no longer bearer secrets.
- Sessions are revoked when the supervised agent exits.
- `gh` access in managed containers is forced through brokered token minting.
- Existing `session_status` and `revoke_session` wire fields remain compatible;
  clients may still send `bind_secret`, but the broker ignores it.

**Costs:**
- The launcher must own child process supervision and signal forwarding.
- The bind fd must be explicitly passed with `exec.Cmd.ExtraFiles`; inheriting
  arbitrary non-CLOEXEC descriptors is no longer sufficient.
- Same-UID local processes can query or revoke another session if they know the
  session ID. This is acceptable for lifecycle operations because same-UID local
  processes already share the user's authority, while token minting still
  requires the memfd binding secret.

## Out of scope

- Cross-user session management. The broker rejects peers with a different UID.
- Persisting or recovering binding secrets after launcher exit.
- Supporting unmanaged `gh` access inside managed devcontainers.
