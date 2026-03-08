# AI Agent Authentication Architecture

## Overview

This document captures the design progression for securing GitHub App-based authentication in the `ai-agent` system. It also incorporates security hardening and containerization guidance with a Podman-first stance.

---

## Table of Contents

1. [Current Problem](#current-problem)
2. [Option 2: Git Credential Helper (No-Cache by Default)](#option-2-git-credential-helper-no-cache-by-default)
3. [The Broker (ssh-agent) Model](#the-broker-ssh-agent-model)
4. [Container / MicroVM Architecture (Podman-First)](#container--microvm-architecture-podman-first)
5. [Review Follow-ups and Deferred Recommendations](#review-follow-ups-and-deferred-recommendations)
6. [Trade-offs Summary](#trade-offs-summary)
7. [Sources](#sources)

---

## Current Problem

The `ai-agent run` script generates a GitHub App installation token once at launch and bakes it into environment variables and git config:

```
ai-agent run --agent claude ...
  ├── generate JWT (valid 10 min) [GH_JWT]
  ├── exchange JWT → installation token (valid 1 hour) [GH_INSTALL_TOKEN]
  ├── bake token into:
  │     git config http.https://github.com/.extraheader  ← static, expires
  │     export GH_TOKEN=...                               ← static, expires
  └── exec claude                                         ← runs for hours
```

After the token expires, both `git push` and `gh pr create` fail with 401s. The agent may fall back to whatever ambient credentials exist (SSH key or personal `gh` OAuth), which can cause PRs to appear under the wrong identity.

---

## Option 2: Git Credential Helper (No-Cache by Default)

Replace the static token with a credential helper script that generates a fresh token on every git auth request. This uses no on-disk token caching and avoids storing tokens in repo config.

### Setup flow

```
ai-agent run --agent claude ...
  ├── write credential helper script to $XDG_RUNTIME_DIR/ai-agent-helper
  ├── do NOT set http.extraheader (must be removed if present)
  ├── configure helper per-process (GIT_CONFIG_COUNT) to avoid repo state changes
  ├── install PATH-level gh wrapper (not a shell function)
  └── exec claude
```

### The helper script

```bash
#!/usr/bin/env bash
# Called by git with "get" on stdin. Generates a fresh installation token.
set -euo pipefail

action="${1:-}"
[[ "$action" == "get" ]] || exit 0

# Read the request from git (protocol=https, host=github.com, etc.)
while IFS='=' read -r key value; do
  [[ -z "$key" ]] && break
  case "$key" in
    host) host="$value" ;;
  esac
done

# Generate fresh JWT → installation token (latency depends on network/API)
jwt="$(generate_jwt "$APP_ID" "$KEY_FILE")"
token="$(get_installation_token "$jwt" "$REPO_SLUG" "$host")"

# Respond in git credential protocol format
printf 'protocol=https\nhost=%s\nusername=x-access-token\npassword=%s\npassword_expiry_utc=%d\nephemeral=true\n' \
  "$host" "$token" "$(( $(date +%s) + 3600 ))"
```

### Key credential protocol fields

| Field | Value | Purpose |
|-------|-------|---------|
| `username` | `x-access-token` | GitHub's expected username for App tokens |
| `password` | fresh installation token | Generated on demand, always valid |
| `password_expiry_utc` | now + 3600 | Expiration hint to git [GIT_CREDENTIAL] |
| `ephemeral` | `true` | Hint to avoid caching (helper-specific behavior) [GIT_CREDENTIAL] |

### `gh` CLI integration

`gh` does not use git credential helpers. It reads `GH_TOKEN` directly. Use a PATH wrapper rather than a shell function, so all processes (not just shells) are covered.

```bash
#!/usr/bin/env bash
# /path/to/wrappers/gh
set -euo pipefail
unset GH_TOKEN GITHUB_TOKEN
GH_TOKEN="$(ai-agent get-token --agent "$AGENT_IDENTITY" --repo-dir .)"
GITHUB_TOKEN="$GH_TOKEN" exec /usr/bin/gh "$@"
```

### Request flow

```
Agent runs for 3 hours, then does git push:

  git push origin feat/branch
    ├── git needs credentials for https://github.com
    ├── git calls: /run/user/1000/ai-agent-helper get
    │     ├── reads APP_ID, KEY_FILE from env/embedded vars
    │     ├── generates JWT (RS256, valid 10 min)
    │     ├── POST /repos/{owner}/{repo}/installation → installation_id
    │     ├── POST /app/installations/{id}/access_tokens → fresh token
    │     └── prints: username=x-access-token, password=<token>, ephemeral=true
    ├── git uses the token for this push
    └── git does NOT call "approve" (because ephemeral=true)
```

### Security considerations

**Private key exposure:**

| Approach | Risk | Mitigation |
|----------|------|------------|
| Helper reads key path | Key path visible to same-UID processes | Helper in `$XDG_RUNTIME_DIR` (tmpfs), mode 0700 |
| Helper reads key file | Key readable by agent | Prefer broker model to remove key from agent context |

**Critical: `extraheader` must be removed.** If auth is set via `http.extraheader`, credential helpers are bypassed. [EXTRAHEADER_ISSUE]

```bash
git config --local --unset "http.https://${github_host}/.extraheader" 2>/dev/null || true
```

### Limitations

1. Latency depends on network and GitHub API responsiveness.
2. GitHub API rate limits apply; installation tokens use the app installation rate limit. [GH_RATE_LIMITS]
3. `gh` requires a wrapper (no native helper support).
4. `password_expiry_utc` and `ephemeral` are part of the git-credential protocol; ensure your git version supports these fields. [GIT_CREDENTIAL]
5. The helper still needs access to PEM material unless a broker is used.

---

## The Broker (ssh-agent) Model

The real limitation of Option 2 is that the PEM private key remains accessible to the agent process. The broker model removes the key from the agent context by holding it in a separate process, similar to `ssh-agent`.

### Architecture

```
                    ┌──────────────────────────────┐
                    │        ai-agent-broker        │
                    │                               │
                    │  PEM keys loaded in memory    │
                    │  (claude.pem, codex.pem, ...) │
                    │                               │
                    │  Listens on Unix socket:      │
                    │  $XDG_RUNTIME_DIR/ai-agent.sock│
                    │  (mode 0600, owner-only)      │
                    │                               │
                    │  On request:                  │
                    │   1. Generate JWT (in-memory)  │
                    │   2. Call GitHub API            │
                    │   3. Return installation token │
                    │   4. Log to audit trail         │
                    └──────────┬───────────────────┘
                               │ Unix socket
                 ┌─────────────┼─────────────────┐
                 │             │                  │
          ┌──────┴──────┐ ┌───┴────────┐  ┌──────┴──────┐
          │ claude-code  │ │ codex-cli  │  │ gemini-cli  │
          │              │ │            │  │             │
          │ Has:         │ │ Has:       │  │ Has:        │
          │  AI_AGENT_   │ │  AI_AGENT_ │  │  AI_AGENT_  │
          │  AUTH_SOCK   │ │  AUTH_SOCK │  │  AUTH_SOCK  │
          │              │ │            │  │             │
          │ Does NOT     │ │ Does NOT   │  │ Does NOT    │
          │ have:        │ │ have:      │  │ have:       │
          │  PEM keys    │ │  PEM keys  │  │  PEM keys   │
          │  APP_ID      │ │  APP_ID    │  │  APP_ID     │
          └──────────────┘ └────────────┘  └─────────────┘
```

### Broker policy enforcement

```
Rules (configurable in broker config):
  - claude agent may only request tokens for: maryzam/snowflake-songs
  - per-agent rate limiting and auditing (set thresholds per repo/agent)
  - tokens downscoped to: contents:write, pull_requests:write, metadata:read [GH_INSTALL_TOKEN]
  - all mints logged with: timestamp, agent, repo, caller PID
```

### Agent process environment

```bash
export AI_AGENT_AUTH_SOCK="$XDG_RUNTIME_DIR/ai-agent.sock"
export AGENT_IDENTITY="claude"
# That's it. No GH_TOKEN, no PEM path, no APP_ID.
```

### Simplified credential helper

```bash
#!/usr/bin/env bash
[[ "${1:-}" == "get" ]] || exit 0
read -r _ # consume stdin from git

token="$(echo '{"agent":"'"$AGENT_IDENTITY"'","repo":"'"$REPO_SLUG"'"}' \
  | socat - UNIX-CONNECT:"$AI_AGENT_AUTH_SOCK")"

printf 'protocol=https\nhost=github.com\nusername=x-access-token\npassword=%s\nephemeral=true\n' "$token"
```

### Security notes (hardening)

- Use `SO_PEERCRED` checks in the broker to enforce same-UID access. [SO_PEERCRED]
- Consider `PR_SET_DUMPABLE=0` to reduce `/proc/<pid>/environ` leakage. [PR_SET_DUMPABLE]
- Keep persistent caching disabled by default; allow an optional in-memory burst cache or singleflight request coalescing (for example, 30-300 seconds) to reduce GitHub App rate consumption during repeated `git` or `gh` operations.

---

## Container / MicroVM Architecture (Podman-First)

Package the broker + shims into an isolated environment where the developer enters once and every agent command works. Podman rootless is the default; Docker is a fallback if Podman is unavailable.

### Core insight

```
Today:
  mary@laptop $ ai-agent run --agent claude -- claude
  mary@laptop $ ai-agent run --agent codex -- codex

Proposed:
  mary@laptop $ ai-agent devenv up   # launches Podman container
  mary@devenv $ claude               # just works
  mary@devenv $ codex                # just works
```

### Full architecture (Podman rootless)

```
┌─────────────────────────────────────────────────────────┐
│                    Host (mary@laptop)                    │
│                                                          │
│  PEM keys in:                                            │
│    ~/.config/ai-agent/keys/*.pem                        │
│    (never copied into containers)                        │
│                                                          │
│  ┌────────────────────────────────────────────────────┐  │
│  │              Podman rootless container              │  │
│  │                                                     │  │
│  │  ┌───────────────┐     ai-agent.sock      │        │  │
│  │  │ ai-agent       │◄──────────────────────┐        │  │
│  │  │ broker         │     in user runtime   │        │  │
│  │  │                │     dir (mode 0600)   │        │  │
│  │  │ Keys: NEVER    │                        │        │  │
│  │  │ here. Signs    │                        │        │  │
│  │  │ via host       │                        │        │  │
│  │  │ socket/vsock   │                        │        │  │
│  │  └────────┬────────┘                        │        │  │
│  │           │                                  │        │  │
│  │           │ mounted socket to host broker    │        │  │
│  │           ▼                                  │        │  │
│  │  ┌──────────────┐  ┌──────────┐  ┌────────┐│        │  │
│  │  │ /usr/bin/     │  │ /usr/bin/│  │/usr/bin/││        │  │
│  │  │ claude (shim) │  │ codex   │  │gemini  ││        │  │
│  │  │               │  │ (shim)  │  │(shim)  ││        │  │
│  │  │ Sets identity │  │         │  │        ││        │  │
│  │  │ + cred helper │  │         │  │        ││        │  │
│  │  │ + execs real  │  │         │  │        ││        │  │
│  │  │   agent CLI   │  │         │  │        ││        │  │
│  │  └──────────────┘  └──────────┘  └────────┘│        │  │
│  │                                             │        │  │
│  │  /workspace/ ← bind-mounted repo            │        │  │
│  │  ~/.gitconfig ← generated at boot            │        │  │
│  │  credential.helper → talks to broker socket  │        │  │
│  └─────────────────────────────────────────────┘        │
└──────────────────────────────────────────────────────────┘
```

### Two-layer signing: keys never enter the container

The container broker does not hold PEM keys. It delegates signing to a host-side signer process. This keeps signing material off the container filesystem entirely.

```
Container agent  ──► Container broker  ──► Host signer  ──► GitHub API
   "give me            "sign this           (has PEM         (returns
    a token"            JWT for me"          in memory)       token)
```

### Agent CLI shims (PATH-level wrappers)

```bash
#!/usr/bin/env bash
# /usr/local/bin/claude (shim installed in container image)
set -euo pipefail

export AGENT_IDENTITY="claude"
export GIT_AUTHOR_NAME="claude[bot]"
export GIT_AUTHOR_EMAIL="2961625+maryzam-claude[bot]@users.noreply.github.com"
export GIT_COMMITTER_NAME="$GIT_AUTHOR_NAME"
export GIT_COMMITTER_EMAIL="$GIT_AUTHOR_EMAIL"
export XDG_RUNTIME_DIR="${XDG_RUNTIME_DIR:-/tmp/ai-agent-runtime}"
export AI_AGENT_AUTH_SOCK="$XDG_RUNTIME_DIR/ai-agent.sock"

# Scrub ambient credentials so the broker path is authoritative.
unset GH_TOKEN GITHUB_TOKEN
git config --local --unset-all "http.https://github.com/.extraheader" 2>/dev/null || true

# Configure git helper for this process only (avoid repo state changes)
export GIT_CONFIG_COUNT=1
export GIT_CONFIG_KEY_0="credential.helper"
export GIT_CONFIG_VALUE_0="/usr/local/libexec/ai-agent-credential-helper"

exec /usr/local/bin/claude-code "$@"
```

### Container image (Podman or Docker)

```Dockerfile
FROM ubuntu:24.04

# Agent CLIs (real binaries)
RUN npm install -g @anthropic-ai/claude-code

# ai-agent broker + credential helper + shims
COPY ai-agent-broker            /usr/local/bin/
COPY ai-agent-credential-helper /usr/local/libexec/
COPY shims/claude               /usr/local/bin/claude
COPY shims/codex                /usr/local/bin/codex
COPY shims/gemini               /usr/local/bin/gemini

COPY entrypoint.sh /entrypoint.sh
ENTRYPOINT ["/entrypoint.sh"]
```

```bash
#!/usr/bin/env bash
# entrypoint.sh
set -euo pipefail

export XDG_RUNTIME_DIR="${XDG_RUNTIME_DIR:-/tmp/ai-agent-runtime}"
install -d -m 0700 "$XDG_RUNTIME_DIR"

# Start broker in background (connects to host signer via mounted socket)
ai-agent-broker \
  --socket "$XDG_RUNTIME_DIR/ai-agent.sock" \
  --signer-socket "$XDG_RUNTIME_DIR/host-signer.sock" \
  --identities /run/secrets/identities.json &

while [ ! -S "$XDG_RUNTIME_DIR/ai-agent.sock" ]; do sleep 0.1; done
exec "${@:-bash}"
```

### Secrets injection (never baked into the image)

```bash
# Local development (Podman rootless)
HOST_XDG_RUNTIME_DIR="${XDG_RUNTIME_DIR:?set XDG_RUNTIME_DIR}"
podman run -it \
  --userns=keep-id \
  -e XDG_RUNTIME_DIR=/tmp/ai-agent-runtime \
  -v ~/github/snowflake-songs:/workspace:Z \
  -v ~/.config/ai-agent/identities.json:/run/secrets/identities.json:ro,Z \
  -v "$HOST_XDG_RUNTIME_DIR/ai-agent/host-signer.sock:/tmp/ai-agent-runtime/host-signer.sock:Z" \
  --read-only --cap-drop=ALL --security-opt=no-new-privileges \
  --tmpfs /tmp --tmpfs /home \
  ai-agent-devenv
```

Note: `identities.json` must not embed PEM paths if the container is not meant to know key locations. Use a reduced metadata-only identities file for container mode.

### Rootless Podman operational notes

- `--userns=keep-id` keeps the developer UID/GID stable across the host and container so files written in `/workspace` remain owned by the invoking user instead of a remapped subordinate UID.
- On SELinux hosts, bind mounts commonly need relabeling. Use `:Z` for private mounts or `:z` for shared mounts on the workspace, secrets file, and mounted signer socket as appropriate for the local policy.
- Keep the host signer socket under the host's `$XDG_RUNTIME_DIR` rather than `/run`. This matches a rootless user service model and avoids assuming root-owned runtime directories.

### Platform note

Podman on macOS and Windows runs containers inside a managed Linux VM (`podman machine`). [PODMAN_MACHINE]

### Host signer lifecycle

The host signer should be treated as a user-scoped daemon, not an incidental background process launched by the container.

```bash
systemctl --user enable --now ai-agent-signer.service
```

Recommended behavior:

- Listen on `$XDG_RUNTIME_DIR/ai-agent/host-signer.sock` with mode `0600`, and create the parent directory with mode `0700`.
- Load PEM material into memory at service start, ideally after an interactive unlock flow such as passphrase entry or OS-backed secret retrieval.
- Expose a cheap health check so `ai-agent devenv up` can fail fast if the signer is unavailable instead of letting `git` or `gh` fail later with opaque auth errors.
- Fail closed: if the signer dies or cannot mint a token, the broker should return a clear error and the shims should not fall back to ambient user credentials.

### MicroVM variant (stronger isolation)

```
┌─────────────────────────────────────┐
│            Host                      │
│  ┌──────────────────┐                │
│  │  Host signer      │               │
│  │  (PEM keys in mem)│               │
│  │  listens: vsock   │               │
│  │  CID 2, port 9999 │               │
│  └──────────┬─────────┘               │
│             │ vsock                    │
│  ┌──────────┼───────────────────┐    │
│  │  Firecracker MicroVM         │    │
│  │                               │    │
│  │  ai-agent-broker              │    │
│  │    └── signs via vsock to host│    │
│  │                               │    │
│  │  claude (shim) → broker       │    │
│  │  /workspace (virtiofs mount)  │    │
│  └───────────────────────────────┘    │
└───────────────────────────────────────┘
```

Firecracker runs on Linux with KVM and advertises fast startup times (e.g., ~125ms to user space). [FIRECRACKER]

`AF_VSOCK` provides host-guest communication for virtual machines; availability depends on OS/hypervisor. [VSOCK]

---

## Review Follow-ups and Deferred Recommendations

### `devcontainer.json` is useful, but not the primary control plane

Providing a `devcontainer.json` alongside `ai-agent devenv up` is reasonable for editor attachment and onboarding, but it should remain a compatibility artifact rather than the canonical architecture surface.

Why this recommendation is deferred as the primary mechanism:

- The hard parts of the design are the host signer lifecycle, rootless Podman mount semantics, socket wiring, and auth fail-closed behavior. `devcontainer.json` can describe mounts, but it does not replace those runtime contracts.
- The environment needs to work for non-IDE entry points too, including terminal-only sessions, local automation, and direct agent CLI invocation.
- A CLI-first bootstrap gives one authoritative path for Podman, Docker fallback, and future microVM backends. A matching `devcontainer.json` can layer on top of the same image and mount model without becoming a second control plane.

In practice, the recommended implementation is: keep `ai-agent devenv up` as canonical, and generate or maintain a `devcontainer.json` that reuses the same image, workspace mount, and signer socket contract.

---

## Trade-offs Summary

| Consideration | Option 2 (Credential Helper) | Broker | Container + Broker |
|--------------|------------------------------|--------|--------------------|
| PEM key exposure | Accessible to agent | In broker memory only | On host only (two-layer signing) |
| Token freshness | Always fresh | Always fresh (no cache by default) | Always fresh (no cache by default) |
| Policy enforcement | None | Broker-level | Broker + container isolation |
| Blast radius | Full app signing | Scoped to declared repo | Scoped + kernel-level isolation |
| Complexity | Low | Medium | High |
| Dev friction | None | Start broker once | One `podman run` or `ai-agent devenv up`; optional editor integration can reuse the same image |
| IDE support | Native | Native | Remote Containers / `devcontainer.json` can attach, but are not required |
| Performance | Network/API dependent | Network/API dependent | Container startup is fast; microVM adds boot cost [FIRECRACKER] |
| macOS support | Full | Full | Containers on macOS run via a Linux VM (Podman Machine) [PODMAN_MACHINE] |
| Key rotation | Update key path | Restart broker | Restart host signer only |

### The fundamental shift

`ai-agent` evolves from a CLI wrapper into a dev environment specification. The security model goes from "trust the agent process" to "trust nothing inside the container except the socket."

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
