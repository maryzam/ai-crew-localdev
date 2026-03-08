# AI Agent Authentication Architecture

## Overview

This document defines the recommended authentication architecture for `ai-agent` as a secure local development environment for multiple AI CLIs operating on GitHub repositories through GitHub App identities.

The design target is not just "refresh expired tokens." The target is a mid-to-long term control plane that:

- keeps GitHub App signing keys out of agent processes and out of containers
- makes brokered auth the only usable path for `git` and `gh`
- scopes each session to an allowed identity and repository set
- fails closed when auth infrastructure is unavailable
- supports a single-user local workstation model with multiple agent CLIs
- can later be packaged behind Podman, Docker fallback, and optionally a microVM

The recommended implementation order is:

1. Host signer + host broker + fail-closed shims
2. Containerized developer environment using the same broker contract
3. Optional microVM isolation

Short-lived credential helpers alone are not the target architecture. They are an implementation detail behind the broker boundary.

---

## Table of Contents

1. [Problem Statement](#problem-statement)
2. [Scope and Threat Model](#scope-and-threat-model)
3. [Security Invariants](#security-invariants)
4. [Recommended Architecture](#recommended-architecture)
5. [Identity and Session Model](#identity-and-session-model)
6. [Repository Attestation and Policy](#repository-attestation-and-policy)
7. [Git and `gh` Integration](#git-and-gh-integration)
8. [Fail-Closed Controls](#fail-closed-controls)
9. [Container Architecture (Podman-First)](#container-architecture-podman-first)
10. [Implementation Plan](#implementation-plan)
11. [Open Decisions](#open-decisions)
12. [Trade-offs Summary](#trade-offs-summary)
13. [Sources](#sources)

---

## Problem Statement

The current `ai-agent run` model mints a GitHub App installation token once at process launch and injects it into process environment and git config:

```text
ai-agent run --agent claude ...
  ├── generate JWT
  ├── exchange JWT -> installation token
  ├── export GH_TOKEN=...
  ├── set git http.extraheader=AUTHORIZATION: basic ...
  └── exec agent CLI
```

This creates three structural problems:

1. Long-running agent sessions outlive the one-hour installation token.
2. Agent processes can see enough auth material to use or leak it directly.
3. When the intended token path fails, tooling may fall back to ambient user credentials such as SSH keys, stored `gh` auth, global git helpers, or `.netrc`.

The result is brittle auth and the risk of repository actions being taken under the wrong identity.

---

## Scope and Threat Model

### Primary target environment

- single developer workstation
- one OS user running local AI CLIs such as Codex, Claude Code, and Gemini CLI
- GitHub SaaS repositories accessed over HTTPS
- GitHub App identities used for write operations and PR creation
- local repos bind-mounted into a containerized dev environment later, but not required for phase 1

### Threats this design addresses

- agent process reads or reuses long-lived auth material
- expired token causes fallback to personal credentials
- one agent impersonates another by setting environment variables
- one repo session requests a token for a different repo
- containerized agent reaches host signing material directly
- operational failures degrade into silent auth bypass

### Threats explicitly out of scope

- a fully compromised user account on the host
- kernel compromise
- protection between mutually untrusted same-UID host processes outside the brokered workflow

The design should still reduce blast radius within a same-user workstation model, but it is not a substitute for host compromise resistance.

---

## Security Invariants

These invariants are the acceptance criteria for the architecture:

1. Agent processes must not have direct access to PEM files, GitHub App private key paths, or signing primitives.
2. Containerized agents must not have direct access to the host signer socket.
3. The broker must not trust caller-provided `AGENT_IDENTITY`, repo slug, or host path as authorization inputs by themselves.
4. `git` and `gh` must fail closed when the broker or signer is unavailable.
5. Ambient credentials must be scrubbed or rejected so brokered auth is authoritative.
6. Every minted token must be attributable to a broker-issued session, allowed repo, and requested permission set.
7. Tokens must be short-lived and minted on demand, with no persistent credential cache enabled by default.
8. Policy must be enforced on the host side, not in agent wrappers alone.

---

## Recommended Architecture

### High-level model

```text
agent CLI -> local shim -> credential helper / gh wrapper -> host broker -> host signer -> GitHub
```

### Component roles

#### 1. Host signer

- user-scoped daemon on the host
- loads GitHub App private key material into memory at startup
- signs JWT payloads on request from the host broker only
- never exposed directly to agents or containers

#### 2. Host broker

- user-scoped daemon on the host
- owns policy, auditing, rate limits, repo authorization, and token minting
- communicates with the signer over a separate private channel
- exposes only the broker socket used by agent sessions

#### 3. Session launcher

- starts an agent session for a declared agent identity and repo context
- creates a broker-issued session capability
- injects only non-secret session metadata into the child environment

#### 4. Agent shims

- configure fail-closed `git` and `gh` behavior for the process tree
- do not embed policy
- do not hold signing material

### Recommended topology

```text
                         Host
  ---------------------------------------------------------
  ai-agent-signer
    - PEM in memory
    - private signer socket (not container-mounted)

  ai-agent-broker
    - policy store
    - audit log
    - rate limiting
    - token minting
    - public broker socket for agent sessions

  ai-agent run / ai-agent devenv up
    - creates broker session
    - launches agent CLI with session capability
  ---------------------------------------------------------
                           |
                           | broker socket only
                           v
                   agent process tree
              git credential helper / gh wrapper
```

This is the target architecture for both host-native and containerized execution. Containerization changes packaging, not trust boundaries.

---

## Identity and Session Model

The earlier design treated `AGENT_IDENTITY` as process-provided input. That is insufficient. The host broker must treat agent identity as a session property it issued, not a string the caller claims.

### Session bootstrap

```text
ai-agent run --agent claude --repo /workspace/repo -- claude
  ├── resolve repo path on host
  ├── load local policy for claude
  ├── verify repo is allowed for claude
  ├── create session_id + random session capability
  ├── register allowed repo set and policy in broker memory
  └── exec child with:
        AI_AGENT_AUTH_SOCK=/.../broker.sock
        AI_AGENT_SESSION_ID=...
        AI_AGENT_SESSION_CAP=...
```

### Broker request contract

The broker should authorize token mint requests using:

- authenticated local socket peer identity
- `session_id`
- `session_capability`
- broker-side session state

It should not authorize requests directly from:

- `AGENT_IDENTITY`
- `REPO_SLUG`
- current working directory
- arbitrary JSON sent by the helper

Those values may be used as hints, but never as the primary authorization source.

### Session lifecycle

- sessions are created by `ai-agent run` or `ai-agent devenv up`
- sessions have explicit TTLs and idle expiry
- broker drops session state when the launcher exits or TTL expires
- tokens minted under a session are auditable by session ID

This gives a durable base for multiple agent CLIs without trusting environment variables as identity.

---

## Repository Attestation and Policy

The broker needs a repo authorization model that survives both host-native and containerized runs.

### Policy object

Each allowed agent identity should map to a policy such as:

```json
{
  "agent": "claude",
  "allowed_repos": [
    "maryzam/snowflake-songs",
    "maryzam/ai-crew-localdev"
  ],
  "default_permissions": {
    "contents": "write",
    "pull_requests": "write",
    "metadata": "read"
  },
  "max_token_ttl_seconds": 3600
}
```

### Repo resolution rules

The broker should derive the target repository from trusted context:

1. Resolve the repository from the launcher's declared repo root at session creation time.
2. For `git`, validate the active remote URL for the current repo against the session's allowed repo mapping.
3. For `gh`, require an explicit repo source when outside a repo and honor `-R owner/repo` only if that repo is in the session allowlist.

### Important constraint

The broker must never mint a token for a repo merely because the helper sent `"repo":"owner/name"`.

### Policy enforcement

The broker should enforce:

- per-session allowed repo set
- per-agent allowed repo set
- permission downscoping when creating installation tokens [GH_INSTALL_TOKEN]
- per-session and per-repo rate limits
- audit logging of token mint requests and denials

---

## Git and `gh` Integration

### Git credential helper

`git` integration should use a credential helper, but only as a transport from git to the broker.

The helper should:

- read git credential input
- resolve host and, where possible, repo context
- pass session identifiers to the broker
- print a fresh installation token
- avoid storing credentials on disk

Illustrative flow:

```text
git push
  -> git credential helper get
  -> helper reads protocol/host/path
  -> helper asks broker for repo-scoped token using session capability
  -> broker validates session + repo + policy
  -> broker mints installation token
  -> helper returns username=x-access-token password=<token>
```

The helper must not have access to PEM material.

### `gh` wrapper

`gh` does not use git credential helpers and still needs a wrapper.

The wrapper should:

- clear `GH_TOKEN` and `GITHUB_TOKEN` before minting a fresh token
- resolve repo context from `-R owner/repo`, current repo, or explicit environment set by the launcher
- reject ambiguous invocations rather than guessing
- set `GH_TOKEN` and `GITHUB_TOKEN` only for the `gh` child process

Illustrative wrapper:

```bash
#!/usr/bin/env bash
set -euo pipefail

unset GH_TOKEN GITHUB_TOKEN

repo="$(ai-agent resolve-gh-repo "$@")"
token="$(ai-agent broker-token --session "$AI_AGENT_SESSION_ID" --repo "$repo")"

GH_TOKEN="$token" GITHUB_TOKEN="$token" exec /usr/bin/gh "$@"
```

### Ambiguity rules for `gh`

The wrapper should fail with a clear error when:

- the command is run outside a git repo and no `-R` is provided
- `-R` points to a repo outside the session allowlist
- the current repo remote does not match a known allowed repo

This is stricter than convenience-first behavior, but it is the correct long-term posture for identity-safe automation.

---

## Fail-Closed Controls

Fail-closed behavior must be explicit. Unsetting `GH_TOKEN` alone is not enough.

### Required controls

#### 1. Remove or override credential sources that bypass the broker

- local `http.<url>.extraheader`
- local/global/system `credential.helper`
- stored `gh` authentication
- `.netrc`
- HTTPS URLs embedding credentials
- SSH remotes for workflows expected to use GitHub App auth

If these cannot be removed safely, the shim should refuse to start the session rather than proceed in a mixed-auth state.

#### 2. Process-local git config

Configure `git` auth for the process tree via environment-backed config such as `GIT_CONFIG_COUNT`, not repo mutation.

#### 3. Broker unavailable means operation failure

If the helper or wrapper cannot reach the broker, the command should fail with a clear message. It must not fall back to user credentials.

#### 4. Signer unavailable means broker denial

If the signer is down or unhealthy, the broker should reject mint requests immediately and emit a clear diagnostic.

#### 5. Audit denials as well as successes

Denied requests are part of the security story. They should be logged with reason codes.

### Recommended validation tests

- expired installation token is refreshed successfully
- broker stopped -> `git push` fails closed
- signer stopped -> `gh pr create` fails closed
- SSH remote configured -> session launch rejected or explicitly unsupported
- personal `gh auth` present -> wrapper still uses brokered token only
- malicious helper request for different repo -> broker denies
- malicious process sets different `AGENT_IDENTITY` -> broker ignores it

---

## Container Architecture (Podman-First)

Containerization is useful, but it should reuse the broker contract rather than redefine auth.

### Core rule

Only the broker interface is exposed into the containerized agent environment. The signer interface remains host-private.

### Recommended topology

```text
Host:
  ai-agent-signer
    -> private signer socket

  ai-agent-broker
    -> public broker socket for agent sessions

Container:
  agent shims
    -> mounted broker socket only
    -> no signer socket
    -> no PEM paths
```

### Why this matters

If the signer socket is mounted into the container, any same-UID process in the container can attempt to bypass broker policy and audit controls. That collapses the architecture back into "whoever can reach the signer can mint identity."

### Container entrypoint expectations

- create runtime dir with mode `0700`
- wait for broker socket availability
- export session metadata only
- configure process-local git helper
- start the requested agent CLI

Illustrative shim:

```bash
#!/usr/bin/env bash
set -euo pipefail

unset GH_TOKEN GITHUB_TOKEN
export AI_AGENT_AUTH_SOCK="${AI_AGENT_AUTH_SOCK:?missing broker socket}"
export AI_AGENT_SESSION_ID="${AI_AGENT_SESSION_ID:?missing session id}"
export AI_AGENT_SESSION_CAP="${AI_AGENT_SESSION_CAP:?missing session capability}"

export GIT_CONFIG_COUNT=1
export GIT_CONFIG_KEY_0="credential.helper"
export GIT_CONFIG_VALUE_0="/usr/local/libexec/ai-agent-credential-helper"

exec /usr/local/bin/claude-code "$@"
```

### Rootless Podman notes

- `--userns=keep-id` is still the right default for file ownership on bind mounts
- SELinux relabeling may still require `:Z` or `:z`
- host broker socket should live under the host user's `$XDG_RUNTIME_DIR`
- on macOS and Windows, Podman runs through `podman machine`, so host/container socket wiring needs platform-specific validation [PODMAN_MACHINE]

### Docker fallback

Docker can reuse the same image and shim contract, but the trust model must stay identical:

- no signer socket in container
- no PEM files in container
- only broker socket mounted

### MicroVM direction

MicroVM isolation is a later hardening layer, not phase 1 scope. It should be considered only after the broker/session/policy contract is stable on the host and in containers.

---

## Implementation Plan

### Phase 1: Define the host control plane

Deliverables:

- broker config schema for agent-to-repo policy
- signer interface contract
- broker API contract
- session capability format and TTL rules
- audit log schema

Exit criteria:

- written contract for identity, repo attestation, and denial reasons
- no caller-controlled field is treated as sufficient authorization

### Phase 2: Build host signer and host broker

Deliverables:

- `ai-agent-signer` daemon
- `ai-agent-broker` daemon
- host-only private signer channel
- token minting with permission downscoping
- audit logging and rate limiting

Exit criteria:

- broker can mint repo-scoped installation tokens through the signer
- direct signer access is not exposed to sessions

### Phase 3: Add session launcher and fail-closed shims

Deliverables:

- `ai-agent run` session bootstrap
- process-local git credential helper
- `gh` wrapper
- ambient credential detection and rejection

Exit criteria:

- long-lived agent session can `git push` and `gh pr create`
- broker outage causes explicit failure, not fallback

### Phase 4: Containerize the same contract

Deliverables:

- `ai-agent devenv up`
- Podman image with agent shims
- mounted broker socket only
- health checks for broker availability

Exit criteria:

- containerized agent can use the same broker session model
- signer remains host-private

### Phase 5: Hardening and optional acceleration

Deliverables:

- optional in-memory burst cache or singleflight request coalescing
- structured metrics
- macOS/Windows Podman validation
- optional Docker fallback

Exit criteria:

- rate consumption and latency are acceptable in daily use
- platform-specific failure modes are documented

### Phase 6: Optional microVM packaging

Deliverables:

- microVM bootstrap contract
- host-to-guest broker connectivity plan

Exit criteria:

- treated as additive isolation, not required for the core security model

---

## Open Decisions

These are the remaining questions that materially affect the final implementation:

1. Is the intended steady-state model "single user, trusted workstation, multiple local agents," or do you want to harden for multiple human users sharing the same machine?
2. Should sessions be limited to one repo at a time by default, or do you want an allowlist of multiple repos per session for cross-repo PR workflows?
3. Do you want to support SSH remotes at all, or should the authenticated write path be HTTPS-only for managed sessions?
4. Is Linux the primary execution target, with macOS as a secondary platform, or do you need equal first-class support for both from phase 1?
5. Do you want the broker to support only GitHub App installation tokens, or should the contract be extensible now for future non-GitHub providers?

My recommendation for the current use case is:

- single-user workstation as the explicit threat model
- one repo per session by default, with optional multi-repo allowlists later
- HTTPS-only for managed write sessions
- Linux first, macOS validated in phase 5
- GitHub-only broker contract for now, with internal interfaces designed so provider support can be added later

---

## Trade-offs Summary

| Consideration | Static Token Injection | Helper Only | Host Broker + Signer | Container + Host Broker |
|--------------|------------------------|-------------|----------------------|-------------------------|
| Token freshness | Poor | Good | Good | Good |
| PEM isolation | Poor | Poor | Strong | Strong |
| Policy enforcement | None | None | Strong | Strong |
| Fail-closed posture | Weak | Medium | Strong | Strong |
| Repo/identity attribution | Weak | Weak | Strong | Strong |
| Complexity | Low | Low | Medium | High |
| Long-term suitability | Poor | Limited | Strong | Strong |

### Recommendation

For your stated use case, the mid-to-long term solution is:

1. host signer
2. host broker with session-bound policy
3. fail-closed shims for `git` and `gh`
4. Podman packaging on top of that contract

That architecture is sufficient for a single-user local AI development environment and leaves room for stronger isolation later without changing the core trust model.

---

## Sources

- [GH_JWT] GitHub App JWT validity window.
- [GH_INSTALL_TOKEN] GitHub App installation token TTL and permission scoping.
- [GIT_CREDENTIAL] Git credential protocol fields (`password_expiry_utc`, `ephemeral`).
- [EXTRAHEADER_ISSUE] Evidence that `extraheader` auth can bypass credential helpers.
- [GH_RATE_LIMITS] GitHub REST API rate limits for GitHub App installations.
- [SO_PEERCRED] `SO_PEERCRED` socket option.
- [PR_SET_DUMPABLE] `PR_SET_DUMPABLE` process attribute.
- [PODMAN_MACHINE] Podman on macOS/Windows uses a Linux VM.
- [FIRECRACKER] Firecracker startup and KVM-based architecture.
- [VSOCK] `AF_VSOCK` overview and platform constraints.

[GH_JWT]: https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/generating-a-json-web-token-jwt-for-a-github-app
[GH_INSTALL_TOKEN]: https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/authenticating-as-a-github-app-installation
[GIT_CREDENTIAL]: https://git-scm.com/docs/git-credential
[EXTRAHEADER_ISSUE]: https://github.com/actions/checkout/issues/162
[GH_RATE_LIMITS]: https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api
[SO_PEERCRED]: https://man7.org/linux/man-pages/man7/unix.7.html
[PR_SET_DUMPABLE]: https://www.man7.org/linux/man-pages/man2/PR_SET_DUMPABLE.2const.html
[PODMAN_MACHINE]: https://docs.podman.io/en/latest/markdown/podman-machine.1.html
[FIRECRACKER]: https://firecracker-microvm.github.io/
[VSOCK]: https://kohlschutter.github.io/junixsocket/junixsocket-vsock/index.html
