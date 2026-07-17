# CLI Reference

**Scope: what each command and flag does.** Nothing else. Concepts belong in the doc that owns them — [Setup](setup.md) for configuration, [Using the Container](using-the-container.md) for the container, [Quality Gates](quality-gates.md) for verification, [Observability](observability.md) for telemetry.

## `ai-agent up`

Bootstrap the whole local environment in one command: guided setup when config is missing, broker startup, readiness checks, optional Langfuse, devcontainer launch, interactive shell.

```text
ai-agent up [--workspace <path>] [--project <path>] [--runtime podman|docker] [--build] [--langfuse]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--workspace` | `.` | Host directory containing your repos, mounted at `/workspace` inside the generic container |
| `--project` | _(unset)_ | Path to a single project whose own `.devcontainer` is honored, with the broker overlay injected |
| `--runtime` | `podman` | Container runtime. Use `docker` only as an explicit opt-out. |
| `--build` | `false` | Force rebuild of the devcontainer image (no cache) |
| `--langfuse` | `false` | Start the Langfuse observability stack as a sidecar |

Runs from any directory — the generic devcontainer definition ships inside the binary. If the runtime or the devcontainer CLI is missing, `ai-agent up` offers to install it; when Podman is selected but only Docker is present, it offers to install Podman or use Docker for that run.

## `ai-agent setup`

Interactive first-time configuration. Prompts for the agent name, GitHub App ID, PEM path, and git author identity; queries the GitHub API to discover the installation; lists accessible repositories so you can choose which ones to allow; writes `identities.json` and `policy.json`. Run it again to add another agent.

```text
ai-agent setup [--agent <name>] [--app-id <id>] [--pem <path>] [--git-name <name>] [--git-email <email>] [--installation-id <id>] [--repos all|<owner/repo,...>] [--non-interactive]
```

`--non-interactive` fails instead of prompting; every required value must come from a flag.

## `ai-agent run`

Launch an agent session with brokered auth. The `--` separator is required; everything after it is the agent's own command.

```text
ai-agent run --agent <name> [--repo <path>] [flags] -- <agent-command> [args...]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--agent` | (required) | Agent identity name from `identities.json` |
| `--repo` | `.` | Path to the git repo |
| `--task-ref` | (none) | External task reference used to group related runs, e.g. `github:owner/repo#43` |
| `--broker-sock` | auto | Broker socket path |
| `--credential-helper` | auto | Path to the credential helper binary |
| `--gh-wrapper` | auto | Path to the ai-agent-gh binary |
| `--verify-cmd` | (none) | Shell command to run after the agent; passing output is hidden and failure output is bounded |
| `--max-retries` | `2` | Max retries when verification fails; allowed range is 0 to 10 |
| `--token-warn-at` | `0` | Warn once when native agent telemetry reports this many run tokens |
| `--token-stop-at` | `0` | Stop the agent when native agent telemetry reports this many run tokens |
| `--isolate-home` | `true` | Run the agent with an ephemeral `HOME` that projects only agent login state |

```bash
ai-agent run --agent claude --repo ~/github/my-project -- claude
ai-agent run --agent codex --repo ~/github/backend -- codex
ai-agent run --agent claude --repo . -- claude --model claude-opus-4
```

Works inside the devcontainer and on the bare host. Verification behavior is described in [Quality Gates](quality-gates.md); what each run records is in [Observability](observability.md).

## `ai-agent doctor`

Validate host and devcontainer readiness. Run it first whenever something breaks — it names the failing check and its remediation.

```text
ai-agent doctor [--mode host|container] [--repo <path>] [--broker-sock <path>] [--runtime podman|docker] [--json]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--mode` | `host` | Readiness mode: `host` or `container` |
| `--broker-sock` | auto | Broker socket path |
| `--repo` | CWD | Path to a git repo to validate |
| `--runtime` | `podman` | Container runtime to validate in container mode |
| `--json` | `false` | Emit JSON output |

| Check | What it verifies |
|-------|-----------------|
| `runtime-dir` | `XDG_RUNTIME_DIR` exists and is a directory |
| `broker-socket` | Broker socket exists and is reachable |
| `broker-reachability` | Broker responds to a health check |
| `repo-remote` | Repo uses an HTTPS remote (not SSH) |
| `broker-identities` | Identities file exists and is valid |
| `broker-policy` | Policy file exists and is valid |
| `binary-*` | Required binaries are installed and executable |
| `container-workspace` | `AI_AGENT_WORKSPACE` is set and accessible (container mode) |
| `container-runtime` | Runtime directory is set up for container mounts (container mode) |

## `ai-agent auth status`

Report Claude and Codex CLI login state and how to remediate a missing login. Run it inside the devcontainer, where the agent CLIs and the persistent `/home/dev` volume live. It probes each agent's native login status, never touches brokered GitHub credentials, and always exits successfully.

```text
ai-agent auth status [--json]
```

## `ai-agent session`

```text
ai-agent session list                            # active sessions (reads local session files)
ai-agent session status <session-id> [--broker-sock <path>]
ai-agent session revoke <session-id> [--broker-sock <path>]
```

`session status` shows active state, agent, repo, creation and expiry, last activity, and credential issuance count. `status` and `revoke` query the broker; `list` reads local session files.

Revoking does not kill the agent process — it keeps running, but any subsequent git or `gh` operation fails with `session_not_found`.

## `ai-agent runs`

| Command | Purpose |
|---------|---------|
| `ai-agent runs list` | Recent managed runs (`--json`, `--limit`) |
| `ai-agent runs show <run-id>` | One run, by full ID or unambiguous prefix (`--json`) |
| `ai-agent runs analyze` | Advisory cross-project optimization report |
| `ai-agent runs findings` | Tracked findings and statuses |
| `ai-agent runs findings accept\|dismiss\|reopen <fingerprint>` | Move a finding through its lifecycle |

See [Observability](observability.md) for what these report and the budgets they apply.

## `ai-agent policy`

```text
ai-agent policy init [--output <path>] [--force] [--identities <path>] [--draft]
ai-agent policy validate [--policy <path>]
```

`policy init` generates a policy from your identities. The template has empty `resources` and fails validation, so without `--draft` the command refuses to write it and points you at `ai-agent setup`.

## `ai-agent check`

Run a command with quiet passing output and bounded failure evidence.

```text
ai-agent check [--dir <path>] [--tail-lines <n>] [--keep-success-log] -- <command> [args...]
```

Each log is capped at 10 MiB; the evidence directory keeps at most 20 logs or 20 MiB.

## `ai-agent install`

Install or remove the broker systemd user units.

```text
ai-agent install [--uninstall]
```
