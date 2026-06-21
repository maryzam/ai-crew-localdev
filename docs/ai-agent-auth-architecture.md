# AI Agent Authentication Architecture

## Overview

This document defines the recommended authentication architecture for `ai-agent` as a secure local development environment for multiple AI CLIs operating on GitHub repositories through GitHub App identities.

The design target is not just "refresh expired tokens." The target is a mid-to-long term control plane that:

- keeps GitHub App signing keys out of agent processes and out of containers
- consolidates signing and brokering into a single host daemon to reduce operational complexity
- makes brokered auth the only usable path for `git` and `gh`
- scopes each session to an allowed identity and repository set
- fails closed when auth infrastructure is unavailable
- supports a single-user local workstation model with multiple agent CLIs
- can later be packaged behind Podman, Docker fallback, and optionally a microVM

Short-lived credential helpers alone are not the target architecture. They are an implementation detail behind the broker boundary.

Outstanding implementation and product work is tracked only in
[Product Gap Analysis](gap-analysis.md).

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
10. [Product Gaps](#product-gaps)
11. [Local Decision Records](#local-decision-records)
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
- local repos bind-mounted into the containerized development environment

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
- compromised local shims or helper binaries caused by PATH hijacking, writable install locations, or package compromise

The design should still reduce blast radius within a same-user workstation model, but it is not a substitute for host compromise resistance.

### Trusted local components

Phase 1 assumes the following local components are trusted once installed:

- `ai-agent` launcher
- broker binary
- git credential helper and `gh` wrapper shims

If those components are replaced locally, the broker cannot prove shim integrity by itself. That assumption should stay explicit rather than implied.

---

## Security Invariants

These invariants are the acceptance criteria for the architecture:

1. Agent processes must not have direct access to PEM files, GitHub App private key paths, or signing primitives. PEM material is loaded into the broker process only.
2. Containerized agents must not have direct access to the broker's signing internals. Only the broker socket is exposed.
3. The broker must not trust caller-provided `AGENT_IDENTITY`, repo slug, or host path as authorization inputs by themselves.
4. `git` and `gh` must fail closed when the broker is unavailable.
5. Ambient credentials must be scrubbed or rejected so brokered auth is authoritative.
6. Every minted token must be attributable to a broker-issued session, allowed repo, and requested permission set.
7. Tokens must be short-lived and minted on demand. The broker may maintain an in-memory cache with TTL shorter than token expiry, but no persistent credential cache is enabled by default.
8. Policy must be enforced on the host side, not in agent wrappers alone.
9. The broker must verify the connecting process UID via `SO_PEERCRED` or equivalent on every local socket connection and reject unexpected UIDs.
10. Session-binding secrets must not be exposed as plain environment variables; they must be delivered over an inherited file descriptor or equivalent non-environment channel.
11. Containerization changes packaging, not trust boundaries or authorization semantics.

---

## Recommended Architecture

### High-level model

```text
agent CLI -> local shim -> credential helper / gh wrapper -> host broker -> GitHub
```

### Component roles

#### 1. Host broker

- single user-scoped daemon on the host
- loads GitHub App private key material into memory at startup and performs JWT signing internally
- owns policy, auditing, rate limits, repo authorization, and token minting
- exposes only the session-facing broker socket used by agent sessions
- PEM material is isolated within the broker process and never exposed to agent sessions or containers
- starts via systemd socket activation on first use, or explicitly via `ai-agent run`

A previous revision of this architecture separated signing into a dedicated signer daemon. That separation was removed because:

- both daemons ran as the same UID on the same host, so compromising the broker gave access to the signer socket
- the real security boundary is broker-to-agent, not signer-to-broker
- merging eliminates one daemon, one unix socket, one systemd unit, and one inter-process health-check dependency

#### 2. Session launcher

- starts an agent session for a declared agent identity and repo context
- creates a broker-issued session binding
- injects only non-secret session metadata into the child environment

#### 3. Agent shims

- configure fail-closed `git` and `gh` behavior for the process tree
- do not embed policy
- do not hold signing material

### Recommended topology

```text
                         Host
  ---------------------------------------------------------
  ai-agent-broker
    - PEM in memory (built-in signing)
    - policy store
    - audit log
    - rate limiting
    - token minting
    - in-memory token cache (short TTL)
    - session-facing broker socket (mode 0600, owner-only)
    - started via systemd socket activation or ai-agent run

  ai-agent run / devcontainer initialization
    - creates broker session
    - launches agent CLI with session binding
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

Phase 1 sessions are single-repo sessions. One launched session maps to one allowed repository and one agent identity.

```text
ai-agent run --agent claude --repo /workspace/repo -- claude
  ├── resolve repo path on host
  ├── load local policy for claude
  ├── verify repo is allowed for claude
  ├── create session_id + 32-byte session binding secret from a CSPRNG
  ├── register allowed repo set and policy in broker memory
  └── exec child with:
        AI_AGENT_AUTH_SOCK=/.../broker.sock
        AI_AGENT_SESSION_ID=...
        AI_AGENT_SESSION_BIND_FD=3
```

The session-binding secret is not placed in the process environment. Phase 1 should treat Linux inherited file descriptors as the normative implementation:

- the launcher generates exactly 32 random bytes from a CSPRNG for each session
- the launcher creates a `memfd_create` anonymous file, writes the raw binary secret to it, and passes it as inherited file descriptor `3`
- the broker stores only a hash of that secret in session state, not the secret itself
- helpers and wrappers read the secret from `AI_AGENT_SESSION_BIND_FD` and present it alongside `session_id`
- any text serialization of the secret for diagnostics or fixtures must use base64url rather than inventing ad hoc encodings

#### FD reopen contract

A single agent session typically invokes multiple `git` and `gh` operations over its lifetime, each of which needs to read the binding secret. Because a plain inherited FD shares one file offset across the process tree, the first reader would advance the offset and subsequent readers would hit EOF.

To support repeatable reads, the launcher must use a backing object that is re-openable:

- the launcher creates the FD via `memfd_create(2)` (or a sealed tmpfs file as fallback)
- after writing the secret, the launcher calls `lseek(fd, 0, SEEK_SET)` before exec
- helpers and wrappers must reopen the backing object via `/proc/self/fd/$AI_AGENT_SESSION_BIND_FD` to obtain a private file offset, read the secret, and close their copy
- reopening via `/proc/self/fd/N` creates a new file description with an independent offset, so concurrent helper invocations do not interfere
- the memfd should be sealed with `F_SEAL_SEAL | F_SEAL_WRITE | F_SEAL_SHRINK | F_SEAL_GROW` after the initial write to prevent modification or unseal by the child process tree (`F_SEAL_SEAL` prevents removal of the other seals)

This contract ensures any number of credential helper and wrapper invocations within one session can read the binding material without coordination.

Non-Linux ports may use an equivalent non-environment channel later, but they must preserve the same properties: per-session randomness, no environment exposure, repeatable reads within the session, and broker-side validation against session state.

### Broker request contract

The broker should authorize token mint requests using:

- authenticated local socket peer identity
- `session_id`
- launcher-established session binding secret delivered outside the environment
- broker-side session state

For phase 1, each token mint request should succeed only when all of the following are true:

- `SO_PEERCRED` or equivalent reports the expected local UID
- the provided `session_id` maps to a live broker session
- the provided binding secret matches the broker-stored hash using constant-time comparison
- the session remains bound to the requested repo and permission set under current broker policy

It should not authorize requests directly from:

- `AGENT_IDENTITY`
- `REPO_SLUG`
- current working directory
- arbitrary JSON sent by the helper

Those values may be used as hints, but never as the primary authorization source.

### Session lifecycle

- sessions are created by `ai-agent run`, whether launched on the host or from inside a devcontainer
- sessions have explicit TTLs (recommended default: 8 hours) and idle expiry (recommended default: 1 hour of no token mint requests)
- sessions support explicit revocation before TTL expiry
- revocation and status authorize by `SO_PEERCRED` UID ownership, not the binding secret, so the secret is required only for token mint and is never persisted to disk
- the launcher supervises the agent process and revokes the session when it exits, so a session never outlives its agent
- session binding secrets are per-session and reusable only for that session's lifetime
- broker drops session state when the launcher exits or TTL expires
- broker invalidates the binding secret immediately on revocation, launcher exit, or TTL expiry
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
  "resources": [
    "github:repo:maryzam/snowflake-songs",
    "github:repo:maryzam/ai-crew-localdev"
  ],
  "providers": {
    "github": {
      "installation_id": 12345678,
      "default_permissions": {
        "contents": "write",
        "pull_requests": "write",
        "metadata": "read"
      }
    }
  }
}
```

See [ADR 0001](decisions/0001-credential-generic-broker-api.md) for the
credential-generic policy schema and provider interface.

GitHub App installation tokens expire after one hour [GH_INSTALL_TOKEN]. The broker cannot shorten GitHub's issued token lifetime, so exposure is managed through narrow permissions, fail-closed transport, no persistent caching by default, and explicit session revocation.

### Repo resolution rules

The broker should derive the target repository from trusted context:

1. Resolve the repository from the launcher's declared repo root at session creation time.
2. In phase 1, bind the session to exactly one allowed repo.
3. For `git`, validate the active remote URL for the current repo against that bound repo.
4. For `gh`, require an explicit repo source when outside a repo and honor `-R owner/repo` only if that repo matches the session-bound repo.

### Important constraint

The broker must never mint a token for a repo merely because the helper sent `"repo":"owner/name"`.

### Policy enforcement

The broker should enforce:

- per-session bound repo in phase 1
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
  -> helper asks broker for repo-scoped token using session binding
  -> broker validates session + repo + policy
  -> broker mints installation token
  -> helper returns username=x-access-token password=<token>
```

The helper must not have access to PEM material.

### `gh` wrapper

`gh` does not use git credential helpers and still needs a wrapper.

The wrapper should:

- clear `GH_TOKEN` and `GITHUB_TOKEN` before minting a fresh token
- make one broker request that performs repo validation and token minting atomically
- pass `-R owner/repo` only as a consistency check against the session-bound repo when present
- reject ambiguous invocations rather than guessing
- set `GH_TOKEN` and `GITHUB_TOKEN` only for the `gh` child process

The `broker-gh-token` command receives the full `gh` argument vector after `--` for atomic repo resolution. The broker must only extract `-R owner/repo` from the forwarded arguments and ignore all other flags. It must not attempt full `gh` argument parsing — unrecognized argument patterns should be ignored for repo resolution purposes, not rejected. This bounds the broker's argument parser surface while keeping the atomic validation contract intact.

Illustrative wrapper:

```bash
#!/usr/bin/env bash
set -euo pipefail

unset GH_TOKEN GITHUB_TOKEN

token="$(ai-agent broker-gh-token --session "$AI_AGENT_SESSION_ID" --bind-fd "$AI_AGENT_SESSION_BIND_FD" -- "$@")"

# The real gh is moved off PATH; the wrapper execs it via AI_AGENT_REAL_GH so
# the only "gh" an agent can reach by name is this brokered wrapper (ADR 0002).
GH_TOKEN="$token" GITHUB_TOKEN="$token" exec "${AI_AGENT_REAL_GH:-/usr/bin/gh}" "$@"
```

**Note:** The token is visible in the `gh` child process's `/proc/pid/environ` for the duration of the command. This is acceptable under the current threat model, which does not claim protection against a determined same-UID attacker inspecting `/proc`. The token is short-lived and scoped to a single repo.

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

**Unset or remove:**

- `GH_TOKEN`
- `GITHUB_TOKEN`
- `GH_HOST`
- local `http.<url>.extraheader`
- local/global/system `credential.helper`
- `GIT_ASKPASS`
- `SSH_ASKPASS`
- stored `gh` authentication
- `.netrc`
- HTTPS URLs embedding credentials
- `SSH_AUTH_SOCK`
- `GIT_SSH`
- `GIT_SSH_COMMAND`
- SSH remotes for managed sessions, because phase 1 uses HTTPS-only GitHub App auth

**Force-set:**

- `GIT_TERMINAL_PROMPT=0` — disables interactive credential prompts so git cannot fall back to asking the user when the broker is unavailable

The launcher and container entrypoint should share one canonical scrub list so this policy is enforced consistently. If these sources cannot be removed or overridden safely, the shim should refuse to start the session rather than proceed in a mixed-auth state.

#### 2. Process-local git config

Configure `git` auth for the process tree via environment-backed config such as `GIT_CONFIG_COUNT`, not repo mutation.

#### 3. Broker unavailable means operation failure

If the helper or wrapper cannot reach the broker, the command should fail with a clear message. It must not fall back to user credentials.

#### 4. Audit denials as well as successes

Denied requests are part of the security story. They should be logged with reason codes.

### Recommended validation tests

- expired installation token is refreshed successfully
- broker stopped -> `git push` fails closed
- broker stopped -> `gh pr create` fails closed
- SSH remote configured -> session launch rejected for managed sessions
- personal `gh auth` present -> wrapper still uses brokered token only
- malicious helper request for different repo -> broker denies
- malicious process sets different `AGENT_IDENTITY` -> broker ignores it
- process without inherited bind FD cannot authenticate to broker
- process with wrong or expired binding material on the FD is rejected
- binding material from one session cannot be replayed against a different session
- second read of binding material from the same FD returns the same secret (verifies reopen semantics)

---

## Container Architecture (Podman-First)

Containerization is useful, but it should reuse the broker contract rather than redefine auth.

### Devcontainers vs Custom Wrapper

To simplify local setup while retaining high security and decent agent isolation, **`devcontainer.json`** should be used as the primary mechanism to spin up the containerized environment, rather than a bespoke `ai-agent devenv up` CLI wrapper.

**Trade-offs of using `devcontainer` CLI:**

Advantages (Simplification & Ecosystem):
- **Pre-built Tooling:** Integrates natively with VS Code, GitHub Codespaces, and the open-source `devcontainer` CLI.
- **Unified Configuration:** `devcontainer.json` can natively handle complex Podman rootless arguments (`--userns=keep-id`), socket mounts, and automatic installation of agent CLIs (Claude, Codex).
- **Ready-to-use Dev Environment:** Developers get an immediately usable, preconfigured environment without needing to learn a new orchestrator tool.

Disadvantages (Constraints):
- **Dynamic Host Resolution Friction:** Rootless Podman requires mapping user-specific paths (like `$XDG_RUNTIME_DIR`). While `devcontainer.json` supports `${localEnv:XDG_RUNTIME_DIR}`, misconfigurations on the host can lead to obscure container startup errors.
- **Bootstrapping Checks:** A custom bash wrapper can explicitly check if the host broker socket is alive *before* launching the container. The `devcontainer` CLI will simply mount a dead socket and fail later.

**Mitigation:**
Use `devcontainer.json` as the declarative source of truth for the environment. Run `ai-agent doctor --mode=container` before launching the devcontainer to verify the host runtime, broker socket, and container prerequisites.

The supported workflow is container-first: start the devcontainer, shell into it, and run `ai-agent run` inside the container when you want a managed session.

### Core rule

Only the broker socket is exposed into the containerized agent environment. The broker's internal signing material remains host-private.

### Recommended topology

```text
Host:
  ai-agent-broker
    -> PEM in memory (built-in signing)
    -> session-facing broker socket only

Container:
  agent shims
    -> mounted broker socket only
    -> no PEM paths
```

### Why this matters

Only the session-facing broker socket is mounted into the container. The broker process and its in-memory PEM material remain on the host. If the broker socket were replaced by direct access to signing material, any same-UID process in the container could bypass broker policy and audit controls.

### Container session binding

The containerized workflow uses the same binding contract as host-native sessions. The devcontainer is just the execution environment; the managed session still starts when the user runs `ai-agent run` inside the container.

In practice this means:

- the devcontainer mounts the broker socket and the workspace, but not any bind-secret file
- once inside the container, `ai-agent run` creates the broker session and delivers the bind secret through the same inherited FD path used on the host
- helpers and wrappers continue to read the secret from `/proc/self/fd/$AI_AGENT_SESSION_BIND_FD`
- the container entrypoint does not synthesize or relocate session secrets

This keeps the host and container flows aligned while avoiding a separate container-only handoff model.

### Container entrypoint expectations

- validate the broker socket mount before starting the requested command
- fail fast when the mounted broker socket is missing, not a Unix socket, or not writable by the invoking UID
- explain how to fix a missing or broken host broker socket
- leave session creation to `ai-agent run` inside the container
- do not synthesize or relocate any bind-secret material
- preserve the owner-only socket contract and matching UID mapping across the host/container boundary
- start the requested command only after those checks pass

Illustrative entrypoint:

```bash
#!/usr/bin/env bash
set -euo pipefail

fail() {
  echo >&2 "ai-agent: devcontainer startup check failed: $*"
  exit 1
}

describe_path_type() {
  case "$1" in
    -*)
      echo "unexpected file type"
      ;;
    *)
      if [[ -f "$1" ]]; then
        echo "regular file"
      elif [[ -d "$1" ]]; then
        echo "directory"
      elif [[ -L "$1" ]]; then
        echo "symlink"
      else
        echo "unexpected file type"
      fi
      ;;
  esac
}

sock="${AI_AGENT_AUTH_SOCK:-}"
if [[ -z "$sock" ]]; then
  fail "AI_AGENT_AUTH_SOCK is not set; the devcontainer must mount the host broker socket at /run/ai-agent/broker.sock"
fi

if [[ ! -e "$sock" ]]; then
  fail "broker socket not found at $sock; start the host broker with 'systemctl --user start ai-agent-broker.socket' and relaunch the devcontainer"
fi

if [[ ! -S "$sock" ]]; then
  fail "expected a Unix socket at $sock, found $(describe_path_type "$sock"); fix the devcontainer mount so it points at the host broker socket"
fi

if [[ ! -w "$sock" ]]; then
  owner_uid="$(stat -c '%u' "$sock" 2>/dev/null || echo '?')"
  owner_gid="$(stat -c '%g' "$sock" 2>/dev/null || echo '?')"
  mode="$(stat -c '%a' "$sock" 2>/dev/null || echo '???')"
  current_uid="$(id -u)"
  fail "broker socket at $sock is not writable by uid $current_uid (owner uid $owner_uid, gid $owner_gid, mode $mode); fix the socket ownership or rootless user mapping on the host"
fi

export AI_AGENT_AUTH_SOCK="$sock"

exec "$@"
```

The strict startup checks are intentional and are the default behavior. A relaxed "warn only" mode is not enabled by default because it would hide the exact mount and UID problems this phase is trying to catch.

### Rootless Podman notes

- `--userns=keep-id` is still the right default for file ownership on bind mounts
- SELinux relabeling may still require `:Z` or `:z`
- host broker socket should live under the host user's `$XDG_RUNTIME_DIR`
- if a host runtime directory is bind-mounted into the container, keep the broker socket owner-only instead of widening permissions for convenience
- on macOS and Windows, Podman runs through `podman machine`, so host/container socket wiring needs platform-specific validation [PODMAN_MACHINE]

### Session recovery

The broker holds session state in memory only; the binding secret lives in the
broker and the inherited sealed memfd and is never written to disk (see
[ADR 0002](decisions/0002-session-credential-integrity.md)). The launcher
supervises the agent and revokes the session when it exits, so a session never
outlives its agent.

Consequently, sessions are not recoverable across a broker restart:

- if the broker process crashes or restarts, in-flight sessions are gone; agents get a clear error on the next token request and must be re-launched with `ai-agent run`
- the launcher writes only non-secret management metadata to `$XDG_RUNTIME_DIR/ai-agent/sessions/` (for `ai-agent session list`), not anything that could revive a dead session
- `ai-agent doctor --mode=host` detects a dead broker and provides restart instructions

### Docker fallback

Docker can reuse the same image and shim contract, but the trust model must stay identical:

- no PEM files in container
- only broker socket mounted

## Product Gaps

This document records the authentication design and its trust boundaries.
Security, reliability, usability, and north-star gaps are prioritized in
[Product Gap Analysis](gap-analysis.md).

---

## Local Decision Records

### LDR-001: Single-Repo Sessions in Phase 1

Decision:

- phase 1 sessions are limited to exactly one repository
- a session may mint tokens only for the repo it was bound to at launch

Rationale:

- single-repo sessions keep repo attestation simple and auditable
- they reduce blast radius if a session or helper is abused
- they avoid ambiguous `gh` context resolution and cross-repo confusion bugs in the first implementation
- they let the broker enforce a direct mapping from session -> agent identity -> repo -> permission set

Consequences:

- cross-repo automation is out of scope for phase 1 managed sessions
- workflows involving multiple repos, submodules with separate remotes, or explicit `gh -R other/repo` usage will require a later extension
- the broker, helper, and wrappers can stay strict and reject any repo mismatch instead of trying to infer intent

Criteria for revisit:

- there is a concrete recurring workflow that requires coordinated access to more than one GitHub repo in a single session
- phase 1 single-repo enforcement has proven stable in daily use
- the broker has test coverage for repo attribution, denial logging, and `gh -R` handling
- the design for multi-repo sessions remains explicit and bounded, for example a primary repo plus a small declared allowlist rather than unconstrained workspace inference

### LDR-002: HTTPS-Only Managed Sessions

Decision:

- managed `ai-agent` sessions use HTTPS-only GitHub App authentication in phase 1
- SSH remotes are rejected for managed sessions instead of being supported as an alternate write path

Rationale:

- GitHub App installation tokens naturally support HTTPS git operations
- HTTPS aligns `git` and `gh` with the same brokered token minting path
- SSH support would require a different credential model, typically user SSH keys or deploy keys, which weakens the broker policy boundary and identity attribution model
- keeping one transport simplifies fail-closed behavior and auditability

Consequences:

- managed sessions must use HTTPS remotes for GitHub operations
- existing `git@github.com:` workflows need remote rewriting or must stay outside the managed auth path
- non-GitHub SSH remotes are outside the managed session design

Criteria for revisit:

- a concrete required GitHub workflow is blocked by HTTPS despite brokered GitHub App tokens
- there is a clear SSH credential model that preserves broker-enforced identity, authorization, and auditability
- the team is willing to support and test a second transport-specific auth path without introducing fallback ambiguity

### LDR-003: Platform and Provider Scope

Decision:

- Linux is the only required platform for phase 1
- GitHub is the only required provider for phase 1

Rationale:

- this keeps the first secure implementation focused on the real target environment
- it avoids premature abstraction in the broker contract
- it reduces integration and test surface while the security model is being proven

Consequences:

- macOS validation can happen later and does not constrain phase 1 design
- provider abstraction is deferred until there is a real second-provider need

Criteria for revisit:

- Linux phase 1 is stable in daily use
- there is a concrete need for macOS support or a second VCS/provider backend
- the core broker/session/policy model is mature enough that portability work will not destabilize security controls

### LDR-004: Single-User Workstation Threat Model

Decision:

- the architecture is explicitly designed for a single-user workstation
- hardening for multiple human users sharing the same machine is out of scope for this design

Rationale:

- the intended use case is one developer running multiple local AI agents under the same OS user
- same-user local process isolation can be improved, but it should not be misrepresented as a multi-user security boundary
- keeping the threat model explicit prevents overdesign in phase 1 and keeps the security claims honest

Consequences:

- the broker reduces blast radius within one user's workflow, but does not claim protection against a malicious second human with access to the same account or machine
- future multi-user hardening would require additional OS-level identity separation, socket access controls, and likely a different deployment model

Criteria for revisit:

- the environment needs to support multiple human users on one shared machine
- there is a requirement to isolate agent sessions across different local user accounts
- the project is willing to expand the trust model beyond single-user local development

### LDR-005: Signing Merged into Broker

Decision:

- JWT signing runs inside the broker process, not as a separate signer daemon
- the broker loads PEM material into memory at startup

Rationale:

- in a single-user workstation model, signer and broker run as the same UID on the same host
- if the broker process were compromised, the attacker could reach the signer socket anyway (same UID, reachable socket)
- the real security boundary is between the broker and agent processes, not between signer and broker
- merging eliminates one daemon, one unix socket, one systemd unit, and one inter-process health-check path
- PEM isolation from agents is preserved because agents only interact with the broker socket

Consequences:

- the broker is the single trusted host daemon; its process integrity is the root of trust
- defense-in-depth within the broker can still be achieved via in-process module separation

Criteria for revisit:

- the broker's attack surface expands to the point where in-process PEM exposure is unacceptable
- a multi-user deployment model requires process-level separation between signing and policy enforcement

### LDR-006: Devcontainers as Primary Container Setup

Decision:

- `devcontainer.json` is the canonical mechanism to launch and manage the containerized dev environment
- developers start the devcontainer first, then shell into it and run `ai-agent run` inside the container when they want a managed session
- bespoke wrappers like `ai-agent devenv up` are deprecated or not built
- validation scripts (`ai-agent doctor --mode=container`) run on the host *before* devcontainer launch

Rationale:

- the devcontainer CLI and IDE integrations are industry standard and well understood by developers
- `devcontainer.json` natively supports complex configurations (rootless Podman `runArgs`, mount points) required for the broker socket and workspace mounts without custom bash glue
- keeping `ai-agent run` as the session bootstrap inside the container preserves the same session-binding contract used on the host
- reducing custom orchestrators aligns with the "easy to spin up" and "preconfigured" local setup goals

Consequences:

- developers use their existing `devcontainer` CLI or VS Code to start the environment
- errors in host setup (e.g., missing `$XDG_RUNTIME_DIR` or dead broker socket) may be harder to debug if the container fails to start inside an IDE
- there is one supported container workflow: launch the devcontainer, then run `ai-agent run` inside it

Criteria for revisit:

- the devcontainer spec cannot support a critical security or platform feature (e.g., microVM handoff)
- developer feedback indicates that `devcontainer.json` is too confusing compared to a specialized CLI

---

## Trade-offs Summary

| Consideration | Static Token Injection | Helper Only | Host Broker (built-in signing) | Container + Host Broker |
|--------------|------------------------|-------------|-------------------------------|-------------------------|
| Token freshness | Poor | Good | Good | Good |
| PEM isolation | Poor | Poor | Strong | Strong |
| Policy enforcement | None | None | Strong | Strong |
| Fail-closed posture | Weak | Medium | Strong | Strong |
| Repo/identity attribution | Weak | Weak | Strong | Strong |
| Complexity | Low | Low | Low-Medium | Medium |
| Long-term suitability | Poor | Limited | Strong | Strong |
| Dev friction | Low | Medium | Medium (socket activation helps) | Low (with `devcontainer.json` integration) |

### Recommendation

For your stated use case, the mid-to-long term solution is:

1. host broker with built-in signing and session-bound policy
2. fail-closed shims for `git` and `gh`
3. Podman packaging on top of that contract

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
