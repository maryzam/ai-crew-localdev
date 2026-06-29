# AI Crew Localdev — User Manual

Secure local auth broker for AI coding agents such as Claude Code and Codex.
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

Use `ai-agent up` as the daily entrypoint. It guides missing first-time
configuration, starts the broker, runs readiness checks, optionally starts
Langfuse, launches the devcontainer, and opens a shell.

```bash
git clone https://github.com/maryzam/ai-crew-localdev.git
cd ai-crew-localdev
make install

# Create and install a GitHub App for the agent first.
# On first run, accept the guided setup prompt; it discovers repos through
# GitHub, writes identities.json and policy.json, then continues bootstrapping.
ai-agent up --workspace "$HOME/github" --langfuse

# In the devcontainer shell, your repos from ~/github are mounted at /workspace.
ai-agent run --agent claude --repo /workspace/my-project -- claude
```

The only decision required on the first `ai-agent up` run is whether to launch
guided setup when config is missing. Setup prompts for the agent name, GitHub
App ID, PEM path, git author identity, and the repositories that agent may
access. The broker keeps the PEM on the host; managed sessions receive only
repo-scoped credentials.

Agent CLI login state lives in the devcontainer home volume. Sign in to Claude
Code or Codex once inside the shell, then re-enter later with the command printed
by `ai-agent up`. The same volume is reused even if the container is replaced.
GitHub repo access is separate and stays brokered through `ai-agent run`; do not
run `gh auth login` in the container.

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
make install    # copies binaries to $GOBIN, or ~/.local/bin when GOBIN is unset
```

Verify the install directory is in your `PATH`:

```bash
which ai-agent
which ai-agent-broker
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

For a complete, validated policy, accept the guided setup prompt from
`ai-agent up` on first run, or run `ai-agent setup` directly. Guided setup
discovers your repositories through the GitHub API and writes a ready-to-use
file. If you prefer to hand-edit a starter template, generate a draft from your
identities:

```bash
ai-agent policy init --draft   # writes an incomplete file with empty resources
```

Without `--draft`, `policy init` validates the generated policy and refuses
to write a file the broker would reject (resources are required).

Edit `~/.config/ai-agent/policy.json` to add your repositories:

```json
{
  "schema_version": "2",
  "default_session_ttl": "8h",
  "default_idle_timeout": "1h",
  "agents": {
    "claude": {
      "resources": [
        "github:repo:youruser/repo-one",
        "github:repo:youruser/repo-two"
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
  }
}
```

| Field | Description |
|-------|-------------|
| `schema_version` | Policy schema version (`"2"`) |
| `default_session_ttl` | Max session lifetime, e.g. `"8h"` |
| `default_idle_timeout` | Idle expiry, e.g. `"1h"` |
| `agents.<name>.resources` | Resource URIs the agent may access (`github:repo:owner/name`) |
| `agents.<name>.providers.github.installation_id` | GitHub App installation ID |
| `agents.<name>.providers.github.app_id` | Optional GitHub App ID; falls back to the identity's `app_id` |
| `agents.<name>.providers.github.default_permissions` | Token scopes: `read`, `write`, or `admin` |

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
| `--draft` | `policy init` | Write the generated policy even if it fails validation (resources empty) |
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
ai-agent install
systemctl --user start ai-agent-broker.socket
```

`ai-agent install` writes the user systemd units with the installed
`ai-agent-broker` path, reloads systemd, and enables socket activation. To
remove the broker units later, run `ai-agent install --uninstall`.

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

`ai-agent up` is the single supported entrypoint. It handles first-use
configuration when default config is missing, broker startup, readiness
validation, optional Langfuse startup, container launch, and interactive shell.

```bash
cd ~/ai-crew-localdev        # must run from the checkout (where .devcontainer/ lives)
ai-agent up --workspace ~/github --langfuse
```

> **Two directories are involved:**
>
> - **Checkout directory** — the `ai-crew-localdev` repo clone. This is where you run the command. The CLI finds `.devcontainer/` here to build and launch the container.
> - **Workspace directory** (`--workspace`) — the host directory containing your project repos (e.g. `~/github`). This gets bind-mounted to `/workspace` inside the container. It is **not** the checkout itself.

`ai-agent up` uses Podman by default. To opt out explicitly, pass `--runtime docker`.

If the selected runtime and/or the devcontainer CLI are not installed, `ai-agent up` will detect the missing tools and offer to install what it can interactively. If Podman is the default but Docker is already available, `ai-agent up` now offers a choice: install Podman and continue, or use Docker for that run.

What `ai-agent up` does:

1. Sets `AI_AGENT_WORKSPACE` to the workspace directory (your repos)
2. Preserves your existing `XDG_RUNTIME_DIR` (or sets a default if unset)
3. If default config is missing, offers to run guided setup before broker startup
4. Ensures the broker is running (tries systemd socket activation, falls back to direct start)
5. Optionally starts the Langfuse observability stack (`--langfuse`)
6. Runs readiness checks (runtime dir, broker socket, config, container tooling)
7. Finds the devcontainer config (`.devcontainer/`) by searching from the executable's location, then CWD
8. Runs `devcontainer up` to start the container
9. Opens an interactive bash shell inside the container

`ai-agent up` flags:

| Flag | Default | Description |
|------|---------|-------------|
| `--workspace` | `.` | Path to the host directory containing your repos (mounted at `/workspace` inside the generic image) |
| `--project` | _(unset)_ | Path to a single project. When set, ai-agent honors that project's own `.devcontainer` (its runtimes, services, ports, and `postCreate`) and injects the broker overlay instead of using the generic image |
| `--runtime` | `podman` | Container runtime to use. Use `docker` only as an explicit opt-out. |
| `--build` | `false` | Force rebuild of the devcontainer image (no cache) |
| `--langfuse` | `false` | Start the Langfuse observability stack (see [Langfuse Observability](#langfuse-observability)) |

### Project-aware mode (`--project`)

The generic image carries Go, Node, and Python plus the agent CLIs — good for general work, but it does not provision a project's specific stack (say Ruby + Postgres + Redis). Point `--project` at a repo that has its own `.devcontainer` and ai-agent runs **that** devcontainer — so its features, `dockerComposeFile` services, `forwardPorts`, and `postCreateCommand` all apply — while injecting a broker overlay:

- the host broker socket is bind-mounted at `/run/ai-agent` and `AI_AGENT_AUTH_SOCK` is set;
- the host-installed `ai-agent`, `ai-agent-gh`, and `ai-agent-credential-helper` binaries are bind-mounted onto `PATH`.

```bash
ai-agent up --project ~/github/my-rails-app
```

The injected toolchain comes from the directory of the `ai-agent` binary you ran, so run `make install` first (or run from the build output). If the project has no `.devcontainer`, ai-agent tells you to use `--workspace` for the generic image instead.

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

### Agent CLI Login State

The generic devcontainer has one supported personal state location:
`/home/dev`, backed by the named `ai-agent-home` volume. Claude Code and Codex
store their own sign-in/config state there, so the simplest flow is:

```bash
ai-agent up --workspace ~/github

# Inside the devcontainer shell, run the agent once and complete its sign-in.
ai-agent run --agent claude --repo /workspace/my-project -- claude
ai-agent run --agent codex --repo /workspace/my-project -- codex
```

After you exit and re-enter the devcontainer, the same `/home/dev` volume is
mounted again and the agent CLIs can reuse their personal login state. The
integration suite proves this with Codex's real `login --with-api-key` and
`login status` commands across container replacement. Claude Code requires a
live provider OAuth flow, so its login reuse remains a manual smoke test.

Keep repo credentials out of this personal state. GitHub operations are
governed by the host broker: `git` uses `ai-agent-credential-helper`, and `gh`
uses `ai-agent-gh`. The wrapper rejects credential-writing `gh auth` commands
such as `login`, `setup-git`, and `refresh` in managed sessions so personal
GitHub tokens or credential-helper config are not written into the durable
agent home.

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

The container has `claude` and `codex` CLIs pre-installed. Optional agent CLIs such as Gemini require a custom image or devcontainer extension. All `gh` invocations are automatically routed through the broker wrapper.

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
| `--runtime` | `podman` | Container runtime to validate in container mode: `podman` or `docker` |
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

In the devcontainer the only `gh` on `PATH` is the `ai-agent-gh` wrapper. The
real `gh` binary is moved to a private location (`$AI_AGENT_REAL_GH`,
`/opt/ai-agent/bin/gh`) so an agent cannot reach it by simply typing `gh`. A
process that knows the absolute path can still invoke the unmanaged binary, so
the policy-bypass gap is not yet fully closed; it stays open until an
end-to-end test proves brokered auth succeeds while ambient personal
credentials are rejected. See [Product Gap Analysis](gap-analysis.md). Do not
configure personal `gh` authentication inside the managed container. The
managed `gh` wrapper rejects credential-writing `gh auth` commands before
minting a broker token.

---

## Container Deep Dive

### What's Inside

The container image (Ubuntu 24.04) ships with all dependencies pinned:

| Tool | Version | Details |
|------|---------|---------|
| **Go** | 1.25.0 | Compiled binaries and runtime |
| **Node.js** | 22.11.0 | LTS |
| **Python 3** | System | Entry-point socket probe |
| **git** | System | System package |
| **make** | System | Common project task runner |
| **jq** | System | JSON inspection helper |
| **unzip** | System | Archive extraction helper |
| **gh** | 2.65.0 | Pinned .deb release (wrapped through `ai-agent-gh`) |
| **claude** | 2.1.84 | `@anthropic-ai/claude-code` via npm |
| **codex** | 0.116.0 | `@openai/codex` via npm |
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
| `--userns=keep-id:uid=1000,gid=1000` | Maps the host UID onto the fixed `dev` user for rootless Podman |

Three writable mounts enter the container:
- **Workspace** (`$AI_AGENT_WORKSPACE` → `/workspace`) — your repos
- **Broker socket** (`$XDG_RUNTIME_DIR/ai-agent` → `/run/ai-agent`) — the socket only, no keys
- **Agent home** (`ai-agent-home` volume → `/home/dev`) — agent logins, CLI config, dotfiles

The home volume is the durable location for Claude/Codex login and tool config.
GitHub repo credentials are intentionally not provisioned there: the managed
wrapper ignores stored personal credentials, blocks credential-writing `gh auth`
commands, and keeps the unmanaged `gh` off `PATH`. This is a supported-path
control, not containment against a process that invokes the real binary by
absolute path or makes raw network calls.

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
  --userns=keep-id:uid=1000,gid=1000 \
  --cap-drop=ALL \
  --security-opt=no-new-privileges \
  --read-only \
  --tmpfs=/tmp:rw,noexec,nosuid,size=512m \
  -v ai-agent-home:/home/dev \
  -v "$HOME/github:/workspace:Z" \
  -v "$XDG_RUNTIME_DIR/ai-agent:/run/ai-agent" \
  -e AI_AGENT_AUTH_SOCK=/run/ai-agent/broker.sock \
  --name ai-agent-dev \
  ai-agent-dev \
  bash
```

Run detached and exec in later:

```bash
# Start in background
podman run -d \
  --userns=keep-id:uid=1000,gid=1000 \
  --cap-drop=ALL \
  --security-opt=no-new-privileges \
  --read-only \
  --tmpfs=/tmp:rw,noexec,nosuid,size=512m \
  -v ai-agent-home:/home/dev \
  -v "$HOME/github:/workspace:Z" \
  -v "$XDG_RUNTIME_DIR/ai-agent:/run/ai-agent" \
  -e AI_AGENT_AUTH_SOCK=/run/ai-agent/broker.sock \
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
| `-v ai-agent-home:/home/dev` | Persistent agent home (logins and config survive restarts) |
| `:Z` on volume | SELinux relabel for rootless Podman |

---

## CLI Reference

### `ai-agent up`

Bootstrap the full local dev environment in one command. Must be run from the `ai-crew-localdev` checkout (or with the binary co-located next to `.devcontainer/`).

```
ai-agent up [--workspace <path>] [--project <path>] [--runtime podman|docker] [--build] [--langfuse]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--workspace` | `.` | Host directory containing your repos (mounted at `/workspace` inside the container) |
| `--project` | _(unset)_ | Path to a single project whose own `.devcontainer` is honored, with the broker overlay injected (see [Project-aware mode](#project-aware-mode---project)) |
| `--runtime` | `podman` | Container runtime to use. Use `docker` only as an explicit opt-out. |
| `--build` | `false` | Force rebuild of the devcontainer image |
| `--langfuse` | `false` | Start Langfuse observability stack as a sidecar |

### `ai-agent run`

Launch an agent session with brokered auth.

```
ai-agent run --agent <name> [--repo <path>] [--task-ref <ref>] [--verify-cmd <cmd>] [flags] -- <agent-command> [args...]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--agent` | (required) | Agent identity name from `identities.json` |
| `--repo` | `.` | Path to the git repo |
| `--task-ref` | (none) | Stable external task reference used to group related runs |
| `--broker-sock` | auto | Broker socket path |
| `--credential-helper` | auto | Path to credential helper binary |
| `--gh-wrapper` | auto | Path to ai-agent-gh binary |
| `--verify-cmd` | (none) | Shell command to run after agent exits (e.g. `"make verify"`); enables verify-and-retry loop |
| `--max-retries` | `2` | Max retries when `--verify-cmd` fails |

Each run records local telemetry and can export the same trace through
OTLP/HTTP JSON. Use `ai-agent runs list` and `ai-agent runs show <run-id>` to
inspect history without an observability backend.

### `ai-agent runs list`

List recent managed runs. Use `--json` for structured output and `--limit` to
bound the result.

### `ai-agent runs show <run-id>`

Show one run by full ID or an unambiguous prefix. Use `--json` for the canonical
run summary.

### `ai-agent doctor`

Validate host and devcontainer readiness.

```
ai-agent doctor [--mode host|container] [--repo <path>] [--broker-sock <path>] [--runtime podman|docker] [--json]
```

### `ai-agent install`

Install or remove the broker systemd user units.

```
ai-agent install [--uninstall]
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

Generate a default policy from your identities file. The generated template
has empty `resources` per agent and is rejected by validation; without
`--draft`, the command refuses to write the file and points you at
`ai-agent setup` for a complete configuration.

```
ai-agent policy init [--output <path>] [--force] [--identities <path>] [--draft]
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
| `AI_AGENT_RUN_TELEMETRY_LOG` | `~/.config/ai-agent/run-telemetry.jsonl` | Managed-run telemetry JSONL path; rotated at 10 MiB with one `.1` backup |
| `AI_AGENT_TELEMETRY` | enabled | Set to `0`, `false`, `off`, or `disabled` to disable managed-run telemetry |
| `AI_AGENT_LANGFUSE_HOST` / `LANGFUSE_HOST` | `http://localhost:3000` | Langfuse ingestion host |
| `AI_AGENT_LANGFUSE_PUBLIC_KEY` / `LANGFUSE_PUBLIC_KEY` | (none) | Langfuse public key for managed-run ingestion |
| `AI_AGENT_LANGFUSE_SECRET_KEY` / `LANGFUSE_SECRET_KEY` | (none) | Langfuse secret key for managed-run ingestion |
| `AI_AGENT_OTLP_TRACES_ENDPOINT` | (none) | OTLP base or traces endpoint; `/v1/traces` is appended when absent |
| `AI_AGENT_OTLP_HEADERS` | (none) | Comma-separated, URL-encoded OTLP HTTP headers |
| `OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` | (none) | Standard signal-specific endpoint, used exactly as configured |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | (none) | Standard OTLP base endpoint; `/v1/traces` is appended |
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
| `AI_AGENT_RUN_ID` | Stable managed-run ID shared by local telemetry, Langfuse, and broker audit metadata |
| `AI_AGENT_TASK_REF` | Optional external task reference shared with the managed agent |
| `AI_AGENT_REAL_GH` | Path to real `gh` binary |
| `GIT_TERMINAL_PROMPT=0` | Prevents interactive git prompts (fail-closed) |

When `--broker-sock` is omitted, `ai-agent` uses `AI_AGENT_AUTH_SOCK` only when it contains a non-empty absolute path. Empty or whitespace-only values fall back to the default runtime socket. Other values fail fast with `invalid AI_AGENT_AUTH_SOCK: must be an absolute path`.

### Scrubbed on Session Launch

Removed from the agent environment to prevent credential leakage:

`GH_TOKEN`, `GITHUB_TOKEN`, `GH_ENTERPRISE_TOKEN`, `GITHUB_ENTERPRISE_TOKEN`, `GH_HOST`, `SSH_AUTH_SOCK`, `GIT_SSH`, `GIT_SSH_COMMAND`, `SSH_ASKPASS`, `GIT_ASKPASS`, `GIT_CONFIG_GLOBAL`, `GIT_CONFIG_SYSTEM`, Langfuse and OTLP exporter configuration, parent `GIT_CONFIG_*` chains, and parent managed-session variables.

---

## Langfuse Observability

The repository can deploy a local Langfuse stack, and `ai-agent run` emits
managed-run telemetry on the supported path. Every run gets a stable run ID,
writes local JSONL history, passes `AI_AGENT_RUN_ID` to the agent process, and
adds the same run ID to broker audit metadata for session and token events.

Local telemetry is written to `~/.config/ai-agent/run-telemetry.jsonl` by
default and rotated at 10 MiB with one `.1` backup. Writes and rotation are
serialized across concurrent managed runs, and the log is kept at mode `0600`.
It records run start/finish, project, agent, the strongest model attribution
available from CLI arguments, environment, identity configuration, or agent
type, command start/finish, verification result, retry count, and elapsed time.
Unavailable exact models and usage values are omitted rather than fabricated.
Full agent prompts and full verify commands are not recorded; verify commands
are stored as hashes.

Langfuse ingestion is enabled when `AI_AGENT_LANGFUSE_PUBLIC_KEY` and
`AI_AGENT_LANGFUSE_SECRET_KEY` are set. The default host is
`http://localhost:3000`; override it with `AI_AGENT_LANGFUSE_HOST`. Langfuse
delivery is buffered and flushes when `ai-agent run` exits. If the
endpoint is misconfigured or unavailable, the launcher prints one warning for
the run and keeps local telemetry as the durable fallback. Standard OTLP
endpoint variables are also supported. Exporter endpoints, headers, and
Langfuse credentials are removed before the agent process starts.

### Starting Langfuse with `ai-agent up`

The simplest way to start Langfuse is alongside the dev environment:

```bash
ai-agent up --langfuse --workspace ~/github
```

This launches the full Langfuse stack (Postgres, ClickHouse, Redis, MinIO, Langfuse web + worker) as Docker Compose services before starting the devcontainer. On first run it copies `contrib/langfuse/.env.example` to `contrib/langfuse/.env` — review and change the secrets before production use.

The Langfuse UI is available only on **http://127.0.0.1:3000** once the stack is
healthy. The loopback binding keeps the local bootstrap account off the LAN.

### Starting Langfuse independently

You can also manage the Langfuse stack separately:

```bash
make langfuse-up     # start the stack
make langfuse-down   # stop the stack
```

Starting the stack alone makes the UI available. Managed-run ingestion also
requires Langfuse API keys in the agent environment.

---

## Verify-and-Retry Loop

The `--verify-cmd` flag on `ai-agent run` enables automatic post-task verification. After an agent exits successfully, the specified shell command runs. If it fails, the agent is re-launched automatically.

### Usage

```bash
# Run the quality gate after the agent completes; retry up to 2 times on failure
ai-agent run --agent claude --repo . --verify-cmd "make verify" -- claude

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
- **CI-like workflows**: Ensure `make verify` passes before considering a task complete
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
| `selected runtime podman is not ready` | Install Podman (`sudo apt-get install podman`) and the devcontainer CLI, or rerun explicitly with `ai-agent up --runtime docker ...` if you want Docker instead. |
| `.devcontainer/ not found` | Run from the `ai-crew-localdev` checkout directory, not from your repos directory |
| `broker did not become ready` | Check broker logs: `journalctl --user -u ai-agent-broker -n 20` |
| `readiness checks failed` | Run `ai-agent doctor --mode container --runtime podman` for details on what failed, or switch explicitly to Docker with `--runtime docker` |
| Container started but no shell | Re-enter with `devcontainer exec --docker-path podman --workspace-folder /path/to/ai-crew-localdev bash` (or `--docker-path docker` if you launched with Docker) |
| Container build fails | Ensure Podman or Docker is installed and running |

### Session won't launch

| Symptom | Fix |
|---------|-----|
| `failed to create session` | Broker not running → `systemctl --user start ai-agent-broker.socket` |
| `credential helper not found` | Run `make install`, ensure `~/.local/bin` is in PATH |
| `no agent command specified` | You forgot the `--` separator |
| `resource_not_allowed` | Add `github:repo:owner/name` to `agents.<name>.resources` in policy.json, reload broker |
| `SSH remote not supported` | Change remote to HTTPS: `git remote set-url origin https://github.com/owner/repo.git` |

### Git/gh errors inside a session

| Error | Cause | Fix |
|-------|-------|-----|
| `session_not_found` | Expired or revoked | Re-launch with `ai-agent run` |
| `resource_not_allowed` | Resource not bound to this session or not in agent's policy | Check `AI_AGENT_SESSION_REPO` matches the session's resource; verify the policy lists the resource for this agent |
| `unknown_credential_type` | Wrong `credential_type` for the requested resource | Use the credential type that serves this resource's provider (e.g. `github_app_installation` for `github:repo:*`) |
| `binding_mismatch` | Corrupted binding | Re-launch session |
| `connection refused` | Broker down | Restart broker |
| `rate_limited` | Too many token mints | Wait and retry |

### Container issues

| Symptom | Fix |
|---------|-----|
| `broker socket not found` | Start host broker, verify `$XDG_RUNTIME_DIR` is set |
| Permission denied on socket | Ensure `--userns=keep-id` is in Podman flags |
| Workspace empty at `/workspace` | Set `AI_AGENT_WORKSPACE` on host before starting container |
| Home not writable / logins not saved | `ai-agent-home` volume ownership must match `--userns=keep-id`; recreate the volume |
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
8. Agent processes receive session binding secrets through sealed memfds, not environment variables. Session management (revoke, status) authorizes by peer UID, so the secret is never written to disk.
9. Containers mount only the broker socket — no keys, no PEM files enter the container.
10. Policy is enforced broker-side, not in the shims.
11. Container runs with no capabilities, read-only root, and no-new-privileges.

### Best practices

- Keep each agent's `resources` list as small as practical.
- Review `~/.config/ai-agent/audit.log` periodically.
- Revoke sessions you no longer need: `ai-agent session revoke <id>`.
- Rotate GitHub App PEM keys periodically via GitHub Settings.
- Keep PEM files `chmod 600`.
- Use containerized sessions for maximum isolation.

### Limitations

- Single-user workstation only (same-UID processes share the broker).
- HTTPS remotes only — SSH git operations are not supported.
- Linux only (Phase 1).
