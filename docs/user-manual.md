# AI Crew Localdev — User Manual

Secure local auth broker for AI coding agents (Claude Code, Codex, Gemini CLI).
The host broker manages GitHub App credentials so agent processes never hold signing keys.

---

## Table of Contents

- [Quick Start](#quick-start)
- [Installation](#installation)
  - [Prerequisites](#prerequisites)
  - [Build from Source](#build-from-source)
- [Configuration](#configuration)
  - [GitHub App Setup](#github-app-setup)
  - [Identities File](#identities-file)
  - [Policy File](#policy-file)
  - [Broker Setup](#broker-setup)
- [Day-to-Day Usage](#day-to-day-usage)
  - [Single-Command Bootstrap](#single-command-bootstrap)
  - [Launch a Session (Bare Metal)](#launch-a-session-bare-metal)
  - [Launch the Dev Container Manually](#launch-the-dev-container-manually)
  - [Run Agent Sessions Inside the Container](#run-agent-sessions-inside-the-container)
- [Session Management](#session-management)
- [Readiness Checks](#readiness-checks)
- [How Auth Works Under the Hood](#how-auth-works-under-the-hood)
- [Container Deep Dive](#container-deep-dive)
  - [What's Inside](#whats-inside)
  - [Runtime Hardening](#runtime-hardening)
  - [Build the Image Manually](#build-the-image-manually)
  - [Run with Podman Directly](#run-with-podman-directly)
- [Langfuse Observability](#langfuse-observability)
- [Verify-and-Retry Loop](#verify-and-retry-loop)
- [CLI Reference](#cli-reference)
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

# 2. Create a GitHub App (see Configuration section), then:
mkdir -p ~/.config/ai-agent
# Place identities.json and PEM key (see below)

# 3. Generate and edit policy
ai-agent policy init
vi ~/.config/ai-agent/policy.json   # add your repos to allowed_repos
                                    # set installation_id for each agent

# 4. Set up the broker (systemd socket activation)
mkdir -p ~/.config/systemd/user
cp contrib/systemd/ai-agent-broker.{service,socket} ~/.config/systemd/user/
systemctl --user daemon-reload
systemctl --user enable --now ai-agent-broker.socket

# 5. Bootstrap the full dev environment
#    IMPORTANT: run from the ai-crew-localdev checkout (where .devcontainer/ lives).
#    --workspace is the host directory containing your repos, NOT the checkout itself.
cd ai-crew-localdev
ai-agent up --workspace "$HOME/github"

# 6. You are now in a bash shell inside the devcontainer.
#    Your repos from ~/github are mounted at /workspace.
#    Launch an agent session:
ai-agent run --agent claude --repo /workspace/my-project -- claude

# 7. When you exit the shell, the container keeps running.
#    Re-enter later with:
devcontainer exec --workspace-folder ~/ai-crew-localdev bash
```

---

## Installation

### Prerequisites

| Requirement | Notes |
|-------------|-------|
| **Linux** | Phase 1 is Linux-only |
| **Go 1.25+** | For building from source |
| **git** | With HTTPS remotes configured on your repos |
| **devcontainer CLI** | Required for `ai-agent up` (install via `npm install -g @devcontainers/cli`) |
| **Podman** or **Docker** | Container runtime for devcontainer sessions |
| **systemd** | Recommended for broker socket activation |

### Build from Source

```bash
git clone https://github.com/maryzam/ai-crew-localdev.git
cd ai-crew-localdev
make build
make install    # copies binaries to ~/.local/bin, sets up git hooks
```

Verify `~/.local/bin` is in your `PATH`:

```bash
which ai-agent          # should print ~/.local/bin/ai-agent
which ai-agent-broker   # should print ~/.local/bin/ai-agent-broker
```

The build produces four binaries:

| Binary | Purpose |
|--------|---------|
| `ai-agent` | Main CLI — bootstraps environment, launches sessions, manages policy |
| `ai-agent-broker` | Host daemon — holds keys, mints tokens |
| `ai-agent-credential-helper` | Git credential helper shim |
| `ai-agent-gh` | gh CLI wrapper shim |

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

Validate the policy:

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
# Or send SIGHUP for a live reload without restart:
kill -HUP $(cat $XDG_RUNTIME_DIR/ai-agent/broker.pid)
```

### Broker Setup

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

## Day-to-Day Usage

This is what your daily workflow looks like once installation and configuration are done.

### Single-Command Bootstrap

`ai-agent up` is the single supported entrypoint. It handles everything: broker startup, readiness validation, container launch, and interactive shell.

```bash
cd ~/ai-crew-localdev        # must run from the checkout (where .devcontainer/ lives)
ai-agent up --workspace ~/github
```

> **Two directories are involved:**
>
> - **Checkout directory** — the `ai-crew-localdev` repo clone. This is where you run the command. The CLI finds `.devcontainer/` here to build and launch the container.
> - **Workspace directory** (`--workspace`) — the host directory containing your project repos (e.g. `~/github`). This gets bind-mounted to `/workspace` inside the container. It is **not** the checkout itself.

If Podman (or Docker) and/or the devcontainer CLI are not installed, `ai-agent up` will detect the missing tools and offer to install them interactively.

What `ai-agent up` does:

1. Sets `AI_AGENT_WORKSPACE` to the workspace directory (your repos)
2. Preserves your existing `XDG_RUNTIME_DIR` (or sets a default if unset)
3. Ensures the broker is running (tries systemd socket activation, falls back to direct start)
4. Optionally starts the Langfuse observability stack (`--langfuse`)
5. Runs readiness checks (runtime dir, broker socket, config, container tooling)
6. Finds the devcontainer config (`.devcontainer/`) by searching from the executable's location, then CWD
7. Runs `devcontainer up` to start the container
8. Opens an interactive bash shell inside the container

`ai-agent up` flags:

| Flag | Default | Description |
|------|---------|-------------|
| `--workspace` | `.` | Path to the host directory containing your repos (mounted at `/workspace` inside the container) |
| `--build` | `false` | Force rebuild of the devcontainer image (no cache) |
| `--langfuse` | `false` | Start the Langfuse observability stack (see [Langfuse Observability](#langfuse-observability)) |

Add this to your shell profile for convenience:

```bash
# ~/.bashrc or ~/.zshrc
export AI_AGENT_WORKSPACE="$HOME/github"
```

Then just run:

```bash
cd ai-crew-localdev
ai-agent up
```

When the broker is started by `ai-agent up` directly (not via systemd), it is automatically terminated when `ai-agent up` exits.

### Re-entering the Container

When you exit the shell (or your terminal closes), the container keeps running in the background. You do **not** need to run `ai-agent up` again.

Re-enter with the devcontainer CLI:

```bash
devcontainer exec --workspace-folder ~/ai-crew-localdev bash
```

`ai-agent up` prints this exact command when it starts, so you can copy it.

To find the backing container through the runtime directly:

```bash
# Podman
podman ps --filter "label=devcontainer.local_folder=$HOME/ai-crew-localdev"

# Docker
docker ps --filter "label=devcontainer.local_folder=$HOME/ai-crew-localdev"
```

To stop and remove the container (the name is assigned by devcontainer and may vary):

```bash
# Find the container ID first
CID=$(podman ps -q --filter "label=devcontainer.local_folder=$HOME/ai-crew-localdev")
# Or with Docker:
# CID=$(docker ps -q --filter "label=devcontainer.local_folder=$HOME/ai-crew-localdev")

# Then stop and remove
podman stop "$CID" && podman rm "$CID"
```

### Launch a Session (Bare Metal)

Run an agent directly on your host without a container:

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
| `--verify-cmd` | (none) | Shell command to run after agent exits; enables verify-and-retry loop |
| `--max-retries` | `2` | Max retries when `--verify-cmd` fails |

### Launch the Dev Container Manually

If you prefer to manage the container lifecycle yourself instead of using `ai-agent up`:

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

This starts the devcontainer defined by the current `ai-crew-localdev` checkout. Your actual repos are still mounted from `AI_AGENT_WORKSPACE` into `/workspace`.

Using VS Code: open the project, then **Ctrl+Shift+P → "Dev Containers: Reopen in Container"**.

**Step 4 — Shell into the running container:**

```bash
devcontainer exec --workspace-folder . bash
```

If you are no longer in the `ai-crew-localdev` checkout, replace `.` with the absolute path to that checkout.

Or with Podman directly:

```bash
podman ps --filter "label=devcontainer.local_folder=$(pwd)" --format '{{.Names}}'
podman exec -it <container-name> bash
```

Or with Docker directly:

```bash
docker ps --filter "label=devcontainer.local_folder=$(pwd)"
docker exec -it <container-id-or-name> bash
```

From VS Code: **Terminal → New Terminal** opens a shell inside the container automatically.

You land in `/workspace` which has your repos mounted from `$AI_AGENT_WORKSPACE`.

### Run Agent Sessions Inside the Container

Once inside the container (via `ai-agent up` or manually):

```bash
cd /workspace/my-repo
ai-agent run --agent claude --repo . -- claude
```

The container has `claude`, `codex`, and `gemini` CLIs pre-installed. All `gh` invocations are automatically routed through the broker wrapper.

Typical container workflow:

```bash
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

The `session status` and `session revoke` subcommands accept `--broker-sock` to specify a custom socket path. `session list` reads local session files and does not query the broker.

Revoking a session doesn't kill the agent process — it continues running but any subsequent git/gh operations fail with `session_not_found`.

---

## Readiness Checks

Use `ai-agent doctor` to verify your setup before launching sessions:

```bash
# Check host-native session prerequisites
ai-agent doctor

# Check prerequisites for containerized sessions
ai-agent doctor --mode container

# Validate a specific repo
ai-agent doctor --repo ~/github/my-project

# Machine-readable output
ai-agent doctor --json
```

`ai-agent doctor` flags:

| Flag | Default | Description |
|------|---------|-------------|
| `--mode` | `host` | Readiness mode: `host` or `container` |
| `--broker-sock` | auto | Broker socket path |
| `--repo` | CWD | Path to a git repo to validate |
| `--json` | `false` | Emit JSON output |

The doctor checks:

| Check | What it verifies |
|-------|-----------------|
| `runtime-dir` | `XDG_RUNTIME_DIR` exists and is a directory |
| `broker-socket` | Broker socket exists and is reachable |
| `broker-reachability` | Broker responds to health check |
| `repo-remote` | Repo uses HTTPS remote (not SSH) |
| `broker-identities` | Identities file exists and is valid |
| `broker-policy` | Policy file exists and is valid |
| `binary-*` | Required binaries are installed and executable |
| `container-workspace` | `AI_AGENT_WORKSPACE` is set and accessible (container mode) |
| `container-runtime` | Runtime directory is set up for container mounts (container mode) |

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

The container image (Ubuntu 24.04) ships with all dependencies pinned:

| Tool | Version | Details |
|------|---------|---------|
| **Go** | 1.25.0 | Compiled binaries and runtime |
| **Node.js** | 22.11.0 | LTS |
| **Python 3** | System | python3 + pip + venv |
| **git** | System | System package |
| **gh** | 2.65.0 | Pinned .deb release (wrapped through `ai-agent-gh`) |
| **claude** | 1.0.3 | `@anthropic-ai/claude-code` via npm |
| **codex** | 0.1.2504301705 | `@openai/codex` via npm |
| **gemini** | 0.1.8 | `@google/gemini-cli` via npm |
| **ai-agent** | Built from source | Session launcher |
| **ai-agent-credential-helper** | Built from source | Git credential shim |
| **ai-agent-gh** | Built from source | gh wrapper shim |

Runs as non-root user `dev` (UID 1000). The entrypoint validates broker socket availability and fails fast on broken socket wiring.

Use `scripts/refresh-pins.sh` to check for newer upstream versions of all pinned dependencies.

### Runtime Hardening

The devcontainer applies strict runtime confinement:

| Setting | Effect |
|---------|--------|
| `--cap-drop=ALL` | No Linux capabilities granted |
| `--security-opt=no-new-privileges` | Prevents privilege escalation via setuid, etc. |
| `--read-only` | Immutable root filesystem |
| `--tmpfs=/tmp:rw,noexec,nosuid,size=512m` | Writable scratch, no executable code |
| `--tmpfs=/home/dev:rw,nosuid,size=1g` | Ephemeral user home (resets on restart by design) |
| `--userns=keep-id` | Maps host UID into container for rootless Podman |

Only two writable bind mounts enter the container:
- **Workspace** (`$AI_AGENT_WORKSPACE` → `/workspace`) — your repos
- **Broker socket** (`$XDG_RUNTIME_DIR/ai-agent` → `/run/ai-agent`) — the socket only, no keys

Shell history, tool configs, and dotfiles reset on every container restart. This is intentional — it prevents credential residue from persisting.

### Build the Image Manually

```bash
cd ai-crew-localdev
podman build -f .devcontainer/Dockerfile -t ai-agent-dev .
```

### Run with Podman Directly

For full control over the container lifecycle:

```bash
# Run interactively
podman run -it --rm \
  --userns=keep-id \
  --cap-drop=ALL \
  --security-opt=no-new-privileges \
  --read-only \
  --tmpfs=/tmp:rw,noexec,nosuid,size=512m \
  --tmpfs=/home/dev:rw,nosuid,size=1g \
  -v "$HOME/github:/workspace:Z" \
  -v "$XDG_RUNTIME_DIR/ai-agent:/run/ai-agent" \
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
  --cap-drop=ALL \
  --security-opt=no-new-privileges \
  --read-only \
  --tmpfs=/tmp:rw,noexec,nosuid,size=512m \
  --tmpfs=/home/dev:rw,nosuid,size=1g \
  -v "$HOME/github:/workspace:Z" \
  -v "$XDG_RUNTIME_DIR/ai-agent:/run/ai-agent" \
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
| `--cap-drop=ALL` | Drops all Linux capabilities for minimal attack surface |
| `--read-only` | Prevents writes to the root filesystem |
| `-v .../ai-agent:/run/ai-agent` | Mounts the broker socket directory — no keys enter the container |
| `-e AI_AGENT_AUTH_SOCK=...` | Tells shims where to find the broker |
| `-e AI_AGENT_REAL_GH=...` | Tells the gh wrapper where the real gh binary is |
| `:Z` on volume | SELinux relabel for rootless Podman |

---

## CLI Reference

### `ai-agent up`

Bootstrap the full local dev environment in one command. Must be run from the `ai-crew-localdev` checkout (or with the binary co-located next to `.devcontainer/`).

```
ai-agent up [--workspace <path>] [--build] [--langfuse]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--workspace` | `.` | Host directory containing your repos (mounted at `/workspace` inside the container) |
| `--build` | `false` | Force rebuild of the devcontainer image |
| `--langfuse` | `false` | Start Langfuse observability stack as a sidecar |

### `ai-agent run`

Launch an agent session with brokered auth.

```
ai-agent run --agent <name> [--repo <path>] [--verify-cmd <cmd>] [flags] -- <agent-command> [args...]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--agent` | (required) | Agent identity name from `identities.json` |
| `--repo` | `.` | Path to the git repo |
| `--broker-sock` | auto | Broker socket path |
| `--credential-helper` | auto | Path to credential helper binary |
| `--gh-wrapper` | auto | Path to ai-agent-gh binary |
| `--verify-cmd` | (none) | Shell command to run after agent exits; enables verify-and-retry loop |
| `--max-retries` | `2` | Max retries when `--verify-cmd` fails |
| `--verify-cmd` | (none) | Shell command to run after agent exits (e.g. `"make test"`); enables verify-and-retry loop |
| `--max-retries` | `2` | Max retries when `--verify-cmd` fails |

### `ai-agent doctor`

Validate host and devcontainer readiness.

```
ai-agent doctor [--mode host|container] [--repo <path>] [--broker-sock <path>] [--json]
```

### `ai-agent session list`

List all active sessions (reads local session files).

### `ai-agent session status <session-id>`

Show session details (queries the broker).

```
ai-agent session status <session-id> [--broker-sock <path>]
```

### `ai-agent session revoke <session-id>`

Revoke an active session immediately.

```
ai-agent session revoke <session-id> [--broker-sock <path>]
```

### `ai-agent policy init`

Generate a default policy from your identities file.

```
ai-agent policy init [--output <path>] [--force] [--identities <path>]
```

### `ai-agent policy validate`

Validate a policy file.

```
ai-agent policy validate [--policy <path>]
```

---

## Environment Variables Reference

### User-Configurable

| Variable | Default | Description |
|----------|---------|-------------|
| `AI_AGENT_CONFIG_DIR` | `~/.config/ai-agent` | Config directory override |
| `AI_AGENT_BROKER_SOCKET` | `$XDG_RUNTIME_DIR/ai-agent/broker.sock` | Broker socket path |
| `AI_AGENT_POLICY_PATH` | `~/.config/ai-agent/policy.json` | Policy file path |
| `AI_AGENT_AUDIT_LOG` | `~/.config/ai-agent/audit.log` | Audit log path |
| `AI_AGENT_SESSION_TTL` | from policy or `8h` | Override default session TTL |
| `AI_AGENT_IDLE_TIMEOUT` | from policy or `1h` | Override idle timeout |
| `AI_AGENT_WORKSPACE` | (none) | Directory containing repos, mounted at `/workspace` in the container |
| `XDG_RUNTIME_DIR` | `/run/user/<uid>` | Per-user runtime directory (usually set by systemd) |

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

## Langfuse Observability

Langfuse provides multi-agent observability — tracing prompt activity, scoring agent quality, and grouping all work on an issue into a single session view.

### Starting Langfuse with `ai-agent up`

The simplest way to start Langfuse is alongside the dev environment:

```bash
ai-agent up --langfuse --workspace ~/github
```

This launches the full Langfuse stack (Postgres, ClickHouse, Redis, MinIO, Langfuse web + worker) as Docker Compose services before starting the devcontainer. On first run it copies `contrib/langfuse/.env.example` to `contrib/langfuse/.env` — review and change the secrets before production use.

The Langfuse UI is available at **http://localhost:3000** once the stack is healthy.

### Starting Langfuse independently

You can also manage the Langfuse stack separately:

```bash
make langfuse-up     # start the stack
make langfuse-down   # stop the stack
```

### Agent integration points

| Agent | Integration | Endpoint |
|-------|------------|----------|
| Claude Code | Hooks → OTel SDK | `localhost:3000/api/public/otel` |
| Codex | OTel export | `localhost:3000/api/public/otel` |
| Gemini/Jules | git hook → REST | `localhost:3000/api/public` (curl POST) |
| All agents | git notes | `refs/notes/agent-log` (permanent record) |

See `docs/dev-workflow-architecture.md` for trace identity conventions.

---

## Verify-and-Retry Loop

The `--verify-cmd` flag on `ai-agent run` enables automatic post-task verification. After an agent exits successfully, the specified shell command runs. If it fails, the agent is re-launched automatically.

### Usage

```bash
# Run tests after the agent completes; retry up to 2 times on failure
ai-agent run --agent claude --repo . --verify-cmd "make test" -- claude

# Custom verify command with 1 retry
ai-agent run --agent codex --repo . --verify-cmd "go test ./... && make lint" --max-retries 1 -- codex
```

### How it works

1. The agent runs as a subprocess (instead of replacing the process via exec)
2. When the agent exits with code 0, the verify command runs via `sh -c`
3. If verification passes, the session is revoked and the launcher exits successfully
4. If verification fails and retries remain, the agent is re-launched
5. If the agent exits with a non-zero code, the loop stops immediately (no verification)
6. After all retries are exhausted, the session is revoked and an error is returned

The agent inherits the same scrubbed environment and memfd-based bind secret as in normal mode — no security properties change.

### When to use

- **Batch/headless agents**: Codex or similar agents that run a task and exit
- **CI-like workflows**: Ensure `make test` passes before considering a task complete
- **Prompt iteration**: Agent makes changes → tests run → if failing, agent gets another attempt

Without `--verify-cmd`, behavior is identical to the default `syscall.Exec` path.

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

### `ai-agent up` fails

| Symptom | Fix |
|---------|-----|
| `devcontainer CLI not found` | Install: `npm install -g @devcontainers/cli` (or accept the auto-install prompt) |
| `missing container tooling` | Install Podman (`sudo apt-get install podman`) or Docker, plus devcontainer CLI. `ai-agent up` will offer to install these for you. |
| `.devcontainer/ not found` | Run from the `ai-crew-localdev` checkout directory, not from your repos directory |
| `broker did not become ready` | Check broker logs: `journalctl --user -u ai-agent-broker -n 20` |
| `readiness checks failed` | Run `ai-agent doctor --mode container` for details on what failed |
| Container started but no shell | Re-enter with `devcontainer exec --workspace-folder /path/to/ai-crew-localdev bash` |
| Container build fails | Ensure Podman or Docker is installed and running |

### Session won't launch

| Symptom | Fix |
|---------|-----|
| `failed to create session` | Broker not running → `systemctl --user start ai-agent-broker.socket` |
| `credential helper not found` | Run `make install`, ensure `~/.local/bin` is in PATH |
| `no agent command specified` | You forgot the `--` separator |
| `repo not allowed` | Add repo to `allowed_repos` in policy.json, reload broker |
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
| `broker socket not found` | Start host broker, verify `$XDG_RUNTIME_DIR` is set |
| Permission denied on socket | Ensure `--userns=keep-id` is in Podman flags |
| Workspace empty at `/workspace` | Set `AI_AGENT_WORKSPACE` on host before starting container |
| Shell history lost on restart | By design — `/home/dev` is a tmpfs for security |
| Can't build image | Ensure Podman (or Docker) and buildah are installed |

### Diagnostic checklist

```bash
# 1. Run the built-in doctor
ai-agent doctor
ai-agent doctor --mode container   # if using devcontainer

# 2. Config files exist and are valid
ls -la ~/.config/ai-agent/{identities,policy}.json
ai-agent policy validate

# 3. PEM key is readable
ls -la ~/.config/ai-agent/*.pem   # should be 600

# 4. Broker socket exists
ls -la $XDG_RUNTIME_DIR/ai-agent/broker.sock

# 5. Broker is listening
systemctl --user status ai-agent-broker.socket

# 6. Repo uses HTTPS remote
git -C ~/my-repo remote get-url origin   # must start with https://

# 7. No ambient credentials leaking
env | grep -E "GH_TOKEN|GITHUB_TOKEN|SSH_AUTH_SOCK"

# 8. Audit log (what the broker has been doing)
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
8. Session binding secrets live in sealed memfds — never in environment variables or on disk.
9. Containers mount only the broker socket — no keys, no PEM files enter the container.
10. Policy is enforced broker-side, not in the shims.
11. Container runs with no capabilities, read-only root, and no-new-privileges.

### Best practices

- Keep `allowed_repos` as small as practical for each agent.
- Review `~/.config/ai-agent/audit.log` periodically.
- Revoke sessions you no longer need: `ai-agent session revoke <id>`.
- Rotate GitHub App PEM keys periodically via GitHub Settings.
- Keep PEM files `chmod 600`.
- Use containerized sessions for maximum isolation.

### Limitations

- Single-user workstation only (same-UID processes share the broker).
- HTTPS remotes only — SSH git operations are not supported.
- Linux only (Phase 1).
