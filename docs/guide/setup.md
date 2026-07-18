# Setup

**Scope: everything you do once.** Installing, creating the GitHub App, the two config files the broker reads, running the broker as a service, and where state lives on disk. Day-to-day commands live in [CLI Reference](cli-reference.md); the container itself is [Using the Container](using-the-container.md).

If you just want to get running, the [User Manual](user-manual.md) quick start is the short version of this page.

## Prerequisites

| Requirement | Notes |
|-------------|-------|
| **Linux** | Phase 1 is Linux-only |
| **git** | With HTTPS remotes configured on your repos (SSH remotes are not supported) |
| **Podman** (preferred) or **Docker** | Container runtime for devcontainer sessions. Podman rootless is the supported default; Docker works as a fallback via `--runtime docker`. |
| **devcontainer CLI** | Required for `ai-agent up` (`npm install -g @devcontainers/cli`; `ai-agent up` offers to install it) |
| **Go 1.25+** | Only if you build from source — see [Building From Source](../design/build-from-source.md) |
| **systemd** | Recommended, for broker socket activation |

## Install

### From a release

Releases ship one static multi-call `ai-agent` binary for Linux amd64 and arm64, plus a `SHA256SUMS` file and the install script. The script downloads the artifact for your architecture, verifies its checksum against `SHA256SUMS`, refuses to install on any mismatch, and creates the `ai-agent-broker`, `ai-agent-gh`, and `ai-agent-credential-helper` invocation symlinks.

```bash
curl -fsSLO https://github.com/maryzam/ai-crew-localdev/releases/latest/download/install.sh
sh install.sh latest                 # or a pinned tag like v0.1.0; installs to ~/.local/bin (override with AI_AGENT_INSTALL_DIR)
```

The script resolves `latest` through the GitHub releases API over HTTPS and refuses plain-HTTP release sources outright, since checksums fetched over the same channel as the artifact verify nothing against a network attacker.

The binary is self-contained: it carries the generic devcontainer definition and the Langfuse stack definition inside itself, so `ai-agent up` works from any directory without a source checkout. See [Using the Container](using-the-container.md#what-gets-staged) for what it materializes and where.

### From source

Building and installing from a checkout, the binary layout, and the verify gates are covered in [Building From Source](../design/build-from-source.md).

### The multi-call binary

One binary, `ai-agent`, plus symlinks that select its role by invocation name:

| Invocation name | Purpose |
|--------|---------|
| `ai-agent` | Main CLI — bootstraps the environment, launches sessions, manages policy |
| `ai-agent-broker` | Host daemon — holds keys, issues credentials |
| `ai-agent-credential-helper` | Git credential helper shim |
| `ai-agent-gh` (also `gh`) | gh CLI wrapper shim |

## Create the GitHub App

Each agent identity requires its own GitHub App. The broker signs with the App's private key; the agent never sees it.

1. **GitHub → Settings → Developer settings → GitHub Apps → New GitHub App**
2. Configure:
   - **Name**: e.g. "Claude Code Agent"
   - **Homepage URL**: any valid URL
   - **Webhook**: uncheck "Active"
   - **Permissions**: Repository `Contents` → Read & write; Repository `Pull requests` → Read & write; Repository `Metadata` → Read-only
3. Create the app, then **generate a private key** (PEM) and download it
4. Note the **App ID** from the app settings page
5. **Install** the app on the target repositories. The installation ID appears in the URL: `https://github.com/settings/installations/<installation-id>` — guided setup discovers it for you.

Store the PEM with owner-only permissions:

```bash
mv ~/Downloads/my-app-key.pem ~/.config/ai-agent/claude-app-key.pem
chmod 600 ~/.config/ai-agent/claude-app-key.pem
```

## Configure

All config lives in `~/.config/ai-agent/` (or `$AI_AGENT_CONFIG_DIR`).

`ai-agent setup` writes both files for you — it prompts for the agent name, App ID, PEM path, and git identity, queries GitHub for the installation, and lets you pick the repositories to allow. `ai-agent up` offers to run it when config is missing. The schemas below are for hand-editing and review.

### identities.json — who each agent is

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
| `tool` | no | Agent tool identifier; required at run time when a project manifest allowlist includes this identity |
| `model` | no | Model identifier (informational) |

### policy.json — what each agent may touch

This is the file the broker enforces. An agent can only reach resources listed here.

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

Permission keys: `contents`, `pull_requests`, `metadata`, `issues`, `actions`, `checks`, `deployments`, `environments`, `packages`, `pages`, `security_events`, `statuses`, `workflows`.

To hand-write one instead of using guided setup:

```bash
ai-agent policy init --draft   # starter template with empty resources
ai-agent policy validate       # refuse anything the broker would reject
```

After editing policy, reload the broker:

```bash
systemctl --user restart ai-agent-broker.service
# or reload in place:
kill -HUP $(cat $XDG_RUNTIME_DIR/ai-agent/broker.pid)
```

## Run the broker as a service

Socket activation is the recommended way to run the broker: it starts on demand when the first session connects, and restarts on failure.

```bash
ai-agent install
systemctl --user start ai-agent-broker.socket
```

`ai-agent install` writes the user systemd units with the installed `ai-agent-broker` path, reloads systemd, and enables socket activation. `ai-agent install --uninstall` removes them.

Verify:

```bash
systemctl --user status ai-agent-broker.socket    # Active: active (listening)
ls -la $XDG_RUNTIME_DIR/ai-agent/broker.sock      # srw------- 1 you you ...
```

For debugging, run `ai-agent-broker` in the foreground instead. When `ai-agent up` starts the broker itself (no systemd), it stops the broker when it exits.

## Where things live

| Path | What it is |
|------|------------|
| `~/.config/ai-agent/identities.json` | Agent identities |
| `~/.config/ai-agent/policy.json` | Broker-enforced policy |
| `~/.config/ai-agent/*.pem` | GitHub App private keys. Host only, mode `600`. |
| `~/.config/ai-agent/audit.log` | Every session and credential issuance |
| `~/.config/ai-agent/run-telemetry.jsonl` | Local run history |
| `~/.local/share/ai-agent/devcontainer/<id>/` | Generated generic devcontainer build context, one per workspace (`<id>` derived from `--workspace`) |
| `~/.local/share/ai-agent/langfuse/` | Generated Langfuse stack and its `.env` |
| `$XDG_RUNTIME_DIR/ai-agent/broker.sock` | Broker socket |

## Environment variables

### User-configurable

| Variable | Default | Description |
|----------|---------|-------------|
| `AI_AGENT_CONFIG_DIR` | `~/.config/ai-agent` | Config directory override |
| `AI_AGENT_DATA_DIR` | `~/.local/share/ai-agent` | Generated-asset directory override (devcontainer context, Langfuse stack) |
| `AI_AGENT_DEV_ASSETS_DIR` | (none) | Trusted checkout root that may override the embedded devcontainer/Langfuse assets. Unset means embedded-only; the working directory is never trusted. |
| `AI_AGENT_BROKER_SOCKET` | `$XDG_RUNTIME_DIR/ai-agent/broker.sock` | Broker socket path; the daemon binds it and every client follows it, so setting this one variable moves both sides. Inside a managed session `AI_AGENT_AUTH_SOCK` takes precedence for clients. |
| `AI_AGENT_POLICY_PATH` | `~/.config/ai-agent/policy.json` | Policy file path |
| `AI_AGENT_AUDIT_LOG` | `~/.config/ai-agent/audit.log` | Audit log path |
| `AI_AGENT_RUN_TELEMETRY_LOG` | `~/.config/ai-agent/run-telemetry.jsonl` | Managed-run telemetry JSONL path; rotated at 10 MiB with one `.1` backup |
| `AI_AGENT_TELEMETRY` | enabled | Set to `disabled` to disable managed-run telemetry |
| `AI_AGENT_SESSION_TTL` | from policy or `8h` | Override default session TTL |
| `AI_AGENT_IDLE_TIMEOUT` | from policy or `1h` | Override idle timeout |
| `AI_AGENT_WORKSPACE` | (none) | Directory containing repos, mounted at `/workspace` in the container |
| `XDG_RUNTIME_DIR` | `/run/user/<uid>` | Per-user runtime directory (usually set by systemd) |

### Set automatically by the session launcher

| Variable | Description |
|----------|-------------|
| `AI_AGENT_AUTH_SOCK` | Broker socket path |
| `AI_AGENT_SESSION_ID` | Session UUID |
| `AI_AGENT_SESSION_BIND_FD` | File descriptor number for the sealed memfd holding the binding secret |
| `AI_AGENT_SESSION_REPO` | Bound repository (`owner/repo`) |
| `AI_AGENT_RUN_ID` | Stable managed-run ID shared by local telemetry, Langfuse, and broker audit metadata |
| `AI_AGENT_TASK_REF` | Optional external task reference shared with the managed agent |
| `AI_AGENT_REAL_GH` | Path to the real `gh` binary |
| `GIT_TERMINAL_PROMPT=0` | Prevents interactive git prompts (fail-closed) |

When `--broker-sock` is omitted, `ai-agent` uses `AI_AGENT_AUTH_SOCK` only when it contains a non-empty absolute path. Empty or whitespace-only values fall back to the default runtime socket. Other values fail fast with `invalid AI_AGENT_AUTH_SOCK: must be an absolute path`.

### Scrubbed on session launch

Removed from the agent environment to prevent credential leakage: `GH_TOKEN`, `GITHUB_TOKEN`, `GH_ENTERPRISE_TOKEN`, `GITHUB_ENTERPRISE_TOKEN`, `GH_HOST`, `SSH_AUTH_SOCK`, `GIT_SSH`, `GIT_SSH_COMMAND`, `SSH_ASKPASS`, `GIT_ASKPASS`, `GIT_CONFIG_GLOBAL`, `GIT_CONFIG_SYSTEM`, Langfuse and OTLP exporter configuration, parent `GIT_CONFIG_*` chains, and parent managed-session variables.
