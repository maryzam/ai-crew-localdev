# AI Crew Localdev — User Manual

Secure local auth broker for AI coding agents (Claude Code, Codex, Gemini CLI).
The host broker manages GitHub App credentials so agent processes never hold signing keys.

---

## Table of Contents

- [Quick Start](#quick-start)
- [Installation](#installation)
  - [Prerequisites](#prerequisites)
  - [Build from Source](#build-from-source)
  - [Systemd Broker Setup](#systemd-broker-setup)
- [Configuration](#configuration)
  - [GitHub App Setup](#github-app-setup)
  - [Identities File](#identities-file)
  - [Policy File](#policy-file)
- [Day-to-Day Usage](#day-to-day-usage)
  - [Launch a Session (Bare Metal)](#launch-a-session-bare-metal)
  - [Launch the Dev Container](#launch-the-dev-container)
  - [Shell into a Running Container](#shell-into-a-running-container)
  - [Run Agent Sessions Inside the Container](#run-agent-sessions-inside-the-container)
- [Session Management](#session-management)
- [How Auth Works Under the Hood](#how-auth-works-under-the-hood)
- [Container Deep Dive](#container-deep-dive)
  - [What's Inside](#whats-inside)
  - [Build the Image Manually](#build-the-image-manually)
  - [Run with Podman Directly](#run-with-podman-directly)
  - [Container Security Model](#container-security-model)
- [Environment Variables Reference](#environment-variables-reference)
- [Troubleshooting](#troubleshooting)
- [Security Model](#security-model)

---

## Quick Start

From zero to a working brokered agent session:

```bash
# 1. Build & install
git clone https://github.com/maryzam/ai-crew-localdev.git
cd ai-crew-localdev
make build && make install

# 2. Create GitHub App (see Configuration section), then:
mkdir -p ~/.config/ai-agent
# Place identities.json and PEM key (see below)

# 3. Generate and edit policy
ai-agent policy init
vi ~/.config/ai-agent/policy.json   # add your repos to allowed_repos
                                    # set installation_id for each agent

# 4. Start the broker
mkdir -p ~/.config/systemd/user
cp contrib/systemd/ai-agent-broker.{service,socket} ~/.config/systemd/user/
systemctl --user daemon-reload
systemctl --user enable --now ai-agent-broker.socket

# 5. Launch an agent (requires the agent CLI to be installed separately)
ai-agent run --agent claude --repo ~/my-repo -- claude
```

---

## Installation

### Prerequisites

| Requirement | Notes |
|-------------|-------|
| **Linux** | Phase 1 is Linux-only |
| **Go 1.25+** | For building from source |
| **git** | With HTTPS remote configured on your repos |
| **gh CLI** | [cli.github.com](https://cli.github.com/) |
| **Podman** | Only if using containerized sessions |
| **systemd** | Recommended for broker socket activation |

### Build from Source

```bash
git clone https://github.com/maryzam/ai-crew-localdev.git
cd ai-crew-localdev
make build
make install    # copies to ~/.local/bin
```

Verify `~/.local/bin` is in your `PATH`:

```bash
which ai-agent          # should print ~/.local/bin/ai-agent
which ai-agent-broker   # should print ~/.local/bin/ai-agent-broker
```

The build produces four binaries:

| Binary | Purpose |
|--------|---------|
| `ai-agent` | Main CLI — launches sessions, manages policy |
| `ai-agent-broker` | Host daemon — holds keys, mints tokens |
| `ai-agent-credential-helper` | Git credential helper shim |
| `ai-agent-gh` | gh CLI wrapper shim |

### Systemd Broker Setup

Socket activation is the recommended way to run the broker — it starts on demand when the first session connects and restarts on failure.

```bash
mkdir -p ~/.config/systemd/user
cp contrib/systemd/ai-agent-broker.service ~/.config/systemd/user/
cp contrib/systemd/ai-agent-broker.socket  ~/.config/systemd/user/

systemctl --user daemon-reload
systemctl --user enable ai-agent-broker.socket
systemctl --user start  ai-agent-broker.socket
```

Verify:

```bash
systemctl --user status ai-agent-broker.socket
# Active: active (listening)

ls -la $XDG_RUNTIME_DIR/ai-agent/broker.sock
# srw------- 1 you you ... broker.sock
```

To start the broker manually instead (foreground, useful for debugging):

```bash
ai-agent-broker
```

---

## Configuration

All config lives in `~/.config/ai-agent/` (or `$AI_AGENT_CONFIG_DIR`).

### GitHub App Setup

Each agent identity requires a dedicated GitHub App:

1. **GitHub → Settings → Developer settings → GitHub Apps → New GitHub App**
2. Configure:
   - **Name**: e.g. "Claude Code Agent"
   - **Homepage URL**: any valid URL
   - **Webhook**: uncheck "Active" (not needed)
   - **Permissions**:
     - Repository: `Contents` → Read & write
     - Repository: `Pull requests` → Read & write
     - Repository: `Metadata` → Read-only
3. Create the app, then **generate a private key** (PEM). Download it.
4. Note the **App ID** from the app settings page.
5. **Install** the app on target repositories. Note the **Installation ID** from the URL:
   `https://github.com/settings/installations/<installation-id>`

Store the PEM securely:

```bash
mv ~/Downloads/my-app-key.pem ~/.config/ai-agent/claude-app-key.pem
chmod 600 ~/.config/ai-agent/claude-app-key.pem
```

### Identities File

Create `~/.config/ai-agent/identities.json`:

```json
{
  "schema_version": "ai-agent-identities/v2",
  "agents": {
    "claude": {
      "git_name": "Claude Code",
      "git_email": "claude@users.noreply.github.com",
      "github_host": "github.com",
      "app_id": "123456",
      "app_key": "/home/you/.config/ai-agent/claude-app-key.pem",
      "tool": "claude-code",
      "model": "claude-opus-4"
    },
    "codex": {
      "git_name": "Codex Agent",
      "git_email": "codex@users.noreply.github.com",
      "github_host": "github.com",
      "app_id": "789012",
      "app_key": "/home/you/.config/ai-agent/codex-app-key.pem",
      "tool": "codex",
      "model": "o3"
    }
  }
}
```

| Field | Required | Description |
|-------|----------|-------------|
| `git_name` | yes | Commit author name |
| `git_email` | yes | Commit author email |
| `app_id` | yes | GitHub App ID (string) |
| `app_key` | yes | Absolute path to PEM private key |
| `github_host` | no | Always `github.com` in Phase 1 |
| `tool` | no | Agent tool identifier (informational) |
| `model` | no | Model identifier (informational) |

### Policy File

Generate a default policy from your identities:

```bash
ai-agent policy init
```

Edit `~/.config/ai-agent/policy.json` to add your repositories:

```json
{
  "schema_version": "ai-agent-policy/v1",
  "default_session_ttl": "8h",
  "default_idle_timeout": "1h",
  "agents": {
    "claude": {
      "allowed_repos": [
        "youruser/repo-one",
        "youruser/repo-two"
      ],
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

| Field | Description |
|-------|-------------|
| `default_session_ttl` | Max session lifetime, e.g. `"8h"` |
| `default_idle_timeout` | Idle expiry, e.g. `"1h"` |
| `allowed_repos` | `owner/repo` slugs the agent may access |
| `installation_id` | GitHub App installation ID |
| `default_permissions` | Token scopes: `read`, `write`, or `admin` |

Available permission keys: `contents`, `pull_requests`, `metadata`, `issues`, `actions`, `checks`, `deployments`, `environments`, `packages`, `pages`, `security_events`, `statuses`, `workflows`.

Validate:

```bash
ai-agent policy validate
```

Policy commands accept these flags:

| Flag | Command | Description |
|------|---------|-------------|
| `--output`, `-o` | `policy init` | Output path (default: `~/.config/ai-agent/policy.json`) |
| `--force` | `policy init` | Overwrite existing file |
| `--identities` | `policy init` | Custom identities file path |
| `--policy` | `policy validate` | Custom policy file path |

After editing policy, reload the broker:

```bash
systemctl --user restart ai-agent-broker.service
```

---

## Day-to-Day Usage

This is what your daily workflow looks like once installation and configuration are done.

### Launch a Session (Bare Metal)

Run an agent directly on your host:

```bash
cd ~/github/my-project
ai-agent run --agent claude --repo . -- claude
```

The `--` separator is required. Everything after it is the agent command.

More examples:

```bash
# Explicit repo path
ai-agent run --agent claude --repo ~/github/my-project -- claude

# Codex on a different repo
ai-agent run --agent codex --repo ~/github/backend -- codex

# Pass flags to the agent
ai-agent run --agent claude --repo . -- claude --model claude-opus-4
```

Inside the session, git and gh operations authenticate transparently:

```bash
git push origin main        # uses brokered token
gh pr create --title "Fix"  # uses brokered token
```

`ai-agent run` flags:

| Flag | Default | Description |
|------|---------|-------------|
| `--agent` | (required) | Agent name from `identities.json` |
| `--repo` | `.` | Path to the git repo |
| `--broker-sock` | auto | Custom broker socket path |
| `--credential-helper` | auto | Custom credential helper path |
| `--gh-wrapper` | auto | Custom gh wrapper path |

### Launch the Dev Container

The dev container gives each agent a sandboxed environment with all tools pre-installed — the agent only sees the broker socket, never keys or signing material.
The supported workflow is container-first: start the devcontainer, shell into it, and run `ai-agent run` inside the container when you want a managed session.

**Step 1 — Ensure the broker is running on the host:**

```bash
systemctl --user status ai-agent-broker.socket
# If not active:
systemctl --user start ai-agent-broker.socket
```

**Step 2 — Set required environment variables on the host:**

```bash
export XDG_RUNTIME_DIR=/run/user/$(id -u)
export AI_AGENT_WORKSPACE="$HOME/github"    # directory containing your repos
```

Add these to your `~/.bashrc` or `~/.zshrc` so they persist.

**Step 3 — Build and start the container:**

Using the devcontainer CLI:

```bash
cd ai-crew-localdev
devcontainer up --workspace-folder .
```

Using VS Code: open the project, then **Ctrl+Shift+P → "Dev Containers: Reopen in Container"**.

Using Podman directly (see [Build the Image Manually](#build-the-image-manually) below for full control).
The direct Podman flow uses the same model: it starts the container first, and `ai-agent run` is still launched from inside the container.

### Shell into a Running Container

Once the container is running, open a shell:

```bash
# Find the container name
podman ps --filter label=devcontainer.local_folder --format '{{.Names}}'

# Shell in as the dev user
podman exec -it <container-name> bash
```

With the devcontainer CLI:

```bash
devcontainer exec --workspace-folder . bash
```

From VS Code: **Terminal → New Terminal** opens a shell inside the container automatically.

You land in `/workspace` which has your repos mounted from `$AI_AGENT_WORKSPACE`.

### Run Agent Sessions Inside the Container

Once inside the container:

```bash
cd /workspace/my-repo
ai-agent run --agent claude --repo . -- claude
```

The container has `claude`, `codex`, and `gemini` CLIs pre-installed. All `gh` invocations are automatically routed through the broker wrapper, and the session binding secret is created when `ai-agent run` starts inside the container.
The container has `claude`, `codex`, and `gemini` CLIs pre-installed. All `gh` invocations are automatically routed through the broker wrapper, and the session binding secret is created when `ai-agent run` starts inside the container.

Typical container workflow:

```bash
# Shell into container
podman exec -it ai-agent-dev bash

# Navigate to your repo
cd /workspace/my-project

# Start an agent session
ai-agent run --agent claude --repo . -- claude

# The agent can now:
#   git push/pull       → brokered token
#   gh pr create        → brokered token
#   gh issue list       → brokered token

# When done, exit the agent (Ctrl+C or agent's quit command)
# Then exit the container shell
exit
```

---

## Session Management

```bash
# List all active sessions
ai-agent session list

# Check a session's details
ai-agent session status <session-id>
# Shows: active, agent, repo, created, expires, last activity, token mint count

# Revoke a session immediately
ai-agent session revoke <session-id>
```

The `session status` and `session revoke` subcommands accept `--broker-sock` to specify a custom socket path. `session list` only reads local session files and does not query the broker.

Revoking a session doesn't kill the agent process — it continues running but any subsequent git/gh operations fail with `session_not_found`.

---

## How Auth Works Under the Hood

### Git Operations

When git needs credentials (push, pull, fetch):

1. Git invokes `ai-agent-credential-helper` via the git credential protocol.
2. The helper reads the session binding secret from the inherited sealed memfd (the FD number is in `AI_AGENT_SESSION_BIND_FD`).
3. It calls the broker over the Unix socket to mint a token for the bound repo.
4. The broker validates the session, signs a JWT, exchanges it for a GitHub installation token.
5. The helper returns the token as `x-access-token` / `<token>` in git credential format.
6. Git uses the short-lived token for the HTTPS operation.

Cross-repo operations are denied — you cannot push to a repo the session isn't bound to.

### gh CLI Operations

The `ai-agent-gh` wrapper intercepts all `gh` invocations:

1. Clears `GH_TOKEN` / `GITHUB_TOKEN` from the environment.
2. Mints a fresh token from the broker.
3. Sets `GH_TOKEN` for the real `gh` child process only.
4. Execs the real `gh` binary.

To bypass the wrapper and use personal `gh` credentials:

```bash
/usr/bin/gh auth status
```

---

## Container Deep Dive

### What's Inside

The container image (Ubuntu 24.04) ships with:

| Tool | Details |
|------|---------|
| **Go** | Latest stable (from build stage) |
| **Node.js** | LTS |
| **Python 3** | System python3 + pip + venv |
| **git** | System package |
| **gh** | Official GitHub CLI (wrapped through `ai-agent-gh`) |
| **claude** | `@anthropic-ai/claude-code` via npm |
| **codex** | `@openai/codex` via npm |
| **gemini** | `@google/gemini-cli` via npm |
| **ai-agent** | Session launcher |
| **ai-agent-credential-helper** | Git credential shim |
| **ai-agent-gh** | gh wrapper shim |

Runs as non-root user `dev` (UID 1000). The entrypoint validates broker socket availability, fails fast on broken socket wiring, and then hands off to the requested command; it does not create a managed session by itself.

### Build the Image Manually

```bash
cd ai-crew-localdev

# Build the image
podman build -f .devcontainer/Dockerfile -t ai-agent-dev .
```

### Run with Podman Directly

For full control over the container lifecycle:

```bash
# Run interactively
podman run -it --rm \
  --userns=keep-id \
  -v "$HOME/github:/workspace:Z" \
  -v "$XDG_RUNTIME_DIR/ai-agent/broker.sock:/run/ai-agent/broker.sock" \
  -e AI_AGENT_AUTH_SOCK=/run/ai-agent/broker.sock \
  -e AI_AGENT_REAL_GH=/usr/bin/gh \
  --name ai-agent-dev \
  ai-agent-dev \
  bash
```

Run detached and exec in later:

```bash
# Start in background
podman run -d \
  --userns=keep-id \
  -v "$HOME/github:/workspace:Z" \
  -v "$XDG_RUNTIME_DIR/ai-agent/broker.sock:/run/ai-agent/broker.sock" \
  -e AI_AGENT_AUTH_SOCK=/run/ai-agent/broker.sock \
  -e AI_AGENT_REAL_GH=/usr/bin/gh \
  --name ai-agent-dev \
  ai-agent-dev \
  sleep infinity

# Shell in
podman exec -it ai-agent-dev bash

# When done
podman stop ai-agent-dev && podman rm ai-agent-dev
```

Key Podman flags explained:

| Flag | Why |
|------|-----|
| `--userns=keep-id` | Maps your host UID into the container so file ownership is correct |
| `-v .../broker.sock:...` | Mounts only the broker socket — no keys enter the container |
| `-e AI_AGENT_AUTH_SOCK=...` | Tells shims where to find the broker |
| `-e AI_AGENT_REAL_GH=...` | Tells the gh wrapper where the real gh binary is |
| `:Z` on volume | SELinux relabel for rootless Podman |

### Container Security Model

- Only the broker socket is mounted — no PEM files, no signing keys enter the container.
- `--userns=keep-id` maps the host UID into the container for rootless operation.
- The container cannot access the broker's in-memory keys.
- All `gh` calls go through the wrapper — there is no way to use ambient GitHub credentials.
- There is no separate bind-secret handoff model in the container; `ai-agent run` creates the session binding inside the container using the same FD contract as host-native sessions.

---

## Environment Variables Reference

### User-Configurable

| Variable | Default | Description |
|----------|---------|-------------|
| `AI_AGENT_CONFIG_DIR` | `~/.config/ai-agent` | Config directory override |
| `AI_AGENT_BROKER_SOCKET` | `$XDG_RUNTIME_DIR/ai-agent/broker.sock` | Broker socket path |
| `AI_AGENT_POLICY_PATH` | `~/.config/ai-agent/policy.json` | Policy file path |
| `AI_AGENT_AUDIT_LOG` | `~/.config/ai-agent/audit.log` | Audit log path |
| `AI_AGENT_SESSION_TTL` | `8h` | Override default session TTL |
| `AI_AGENT_IDLE_TIMEOUT` | `1h` | Override idle timeout |
| `AI_AGENT_WORKSPACE` | (none) | Container workspace mount source |

### Set Automatically by Session Launcher

| Variable | Description |
|----------|-------------|
| `AI_AGENT_AUTH_SOCK` | Broker socket path |
| `AI_AGENT_SESSION_ID` | Session UUID |
| `AI_AGENT_SESSION_BIND_FD` | File descriptor number for the sealed memfd holding the binding secret |
| `AI_AGENT_SESSION_REPO` | Bound repository (`owner/repo`) |
| `AI_AGENT_REAL_GH` | Path to real `gh` binary |
| `GIT_TERMINAL_PROMPT=0` | Prevents interactive git prompts (fail-closed) |

### Scrubbed on Session Launch

Removed from the agent environment to prevent credential leakage:

`GH_TOKEN`, `GITHUB_TOKEN`, `GH_HOST`, `SSH_AUTH_SOCK`, `GIT_SSH`, `GIT_SSH_COMMAND`, `SSH_ASKPASS`, `GIT_ASKPASS`, `GIT_CONFIG_GLOBAL`, `GIT_CONFIG_SYSTEM`

---

## Troubleshooting

### Broker won't start

```bash
# Check service status and logs
systemctl --user status ai-agent-broker.service
journalctl --user -u ai-agent-broker.service --no-pager -n 50

# Common causes:
# - Bad PEM path in identities.json → fix the app_key path
# - Invalid policy → run: ai-agent policy validate
# - Socket already exists → remove stale socket, restart
```

### Session won't launch

| Symptom | Fix |
|---------|-----|
| `failed to create session` | Broker not running → `systemctl --user start ai-agent-broker.socket` |
| `credential helper not found` | Run `make install`, ensure `~/.local/bin` is in PATH |
| `no agent command specified` | You forgot the `--` separator |
| `repo not allowed` | Add repo to `allowed_repos` in policy.json, restart broker |
| `SSH remote not supported` | Change remote to HTTPS: `git remote set-url origin https://github.com/owner/repo.git` |

### Git/gh errors inside a session

| Error | Cause | Fix |
|-------|-------|-----|
| `session_not_found` | Expired or revoked | Re-launch with `ai-agent run` |
| `repo_not_allowed` | Wrong repo for this session | Check `AI_AGENT_SESSION_REPO` |
| `binding_mismatch` | Corrupted binding | Re-launch session |
| `connection refused` | Broker down | Restart broker |
| `rate_limited` | Too many token mints | Wait and retry |

### Container issues

| Symptom | Fix |
|---------|-----|
| `broker socket not found` | Start host broker first, verify `$XDG_RUNTIME_DIR` is set |
| Permission denied on socket | Ensure `--userns=keep-id` is in Podman flags |
| Workspace empty | Set `AI_AGENT_WORKSPACE` on host before starting container |
| Can't build image | Ensure Podman and buildah are installed |

### Diagnostic checklist

```bash
# 1. Config files exist and are valid
ls -la ~/.config/ai-agent/{identities,policy}.json
ai-agent policy validate

# 2. PEM key is readable
ls -la ~/.config/ai-agent/*.pem   # should be 600

# 3. Broker socket exists
ls -la $XDG_RUNTIME_DIR/ai-agent/broker.sock

# 4. Broker socket is listening (service activates on demand)
systemctl --user status ai-agent-broker.socket

# 5. Repo uses HTTPS remote
git -C ~/my-repo remote get-url origin   # must start with https://

# 6. No ambient credentials leaking
env | grep -E "GH_TOKEN|GITHUB_TOKEN|SSH_AUTH_SOCK"

# 7. Audit log (what the broker has been doing)
tail -20 ~/.config/ai-agent/audit.log
```

---

## Security Model

### Threat model

This system protects against AI agent processes exfiltrating or misusing GitHub credentials. It does **not** protect against a fully compromised user account or kernel.

### Key invariants

1. Agent processes never have access to PEM files or signing primitives.
2. Tokens are short-lived (GitHub's 1-hour installation token TTL).
3. Each session is bound to one repo — cross-repo access is denied.
4. Fail-closed: when the broker is unreachable, git and gh fail explicitly.
5. Ambient credentials (SSH keys, `gh auth`, `.netrc`) are scrubbed from the session.
6. Every token mint is logged with session, repo, and permission set.
7. Broker validates caller UID via `SO_PEERCRED` on every connection.
8. Session binding secrets live in sealed memfds — never in environment variables.
9. Containers mount only the broker socket — no keys, no PEM files.
10. Policy is enforced broker-side, not in the shims.

### Best practices

- Keep `allowed_repos` as small as practical for each agent.
- Review `~/.config/ai-agent/audit.log` periodically.
- Revoke sessions you no longer need: `ai-agent session revoke <id>`.
- Rotate GitHub App PEM keys periodically via GitHub Settings.
- Keep PEM files `chmod 600`.
- Use containerized sessions for maximum isolation.

### Limitations

- Single-user workstation only (same-UID processes share the broker).
- HTTPS remotes only — SSH git operations are not supported in Phase 1.
- Linux only (Phase 1).
