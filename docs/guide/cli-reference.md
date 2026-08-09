# CLI Reference

**Scope: what each command and flag does.** Nothing else. Concepts belong in the doc that owns them — [Setup](setup.md) for configuration, [Using the Container](using-the-container.md) for the container, [Quality Gates](quality-gates.md) for verification, [Observability](observability.md) for telemetry.

## `ai-agent start`

Start or resume a governed agent in a private checkout of one repository. With no repository argument, the containing Git repository is used. The source checkout must have a clean working tree, a named branch, a committed `HEAD`, and a credential-free GitHub HTTPS or SSH origin. No remote clone or pull occurs: the private checkout is copied locally without hardlinks and mounted alone at `/workspace`.

```text
ai-agent start [repository] [--agent <name>] [--new] [--runtime podman|docker] [--build] [--observability=true|false] [-v] -- [agent-arguments...]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--agent` | sole configured agent | Configured identity to use; required when several agents exist |
| `--new` | `false` | Create another private workspace instead of resuming the active one |
| `--runtime` | `podman` | Container runtime. Use `docker` only as an explicit opt-out. |
| `--build` | `false` | Force rebuild of the managed devcontainer image |
| `--observability` | `true` | Start local Langfuse and authorize sanitized trace publication; set to `false` for local history only |
| `-v`, `--verbose` | `false` | Stream container build output to the terminal instead of the log |

The configured identity selects the executable, so it is not repeated after `--`:

```bash
ai-agent start
ai-agent start ~/github/my-project --agent codex -- --model o3
```

Agent changes remain in the private workspace after the process exits or fails. A workspace is bound to its selected identity and compiled tool; use `--new` rather than silently resuming it as another identity. The source checkout is unchanged until `ai-agent apply` succeeds.

The governed container runtime and exact ID are recorded durably before agent execution. The container is removed before result checkpointing, while the private checkout and agent login volume remain durable. If container removal cannot be proven, checkpointing fails closed and the workspace is retained in a failed state; the next `start` reconciles that container before resuming.

## `ai-agent apply`

Fast-forward a private-workspace result into its source repository. With no option, the active workspace is selected. Use `--workspace <id>` to select an older retained result after `start --new`. The source must still be on the recorded branch and base commit with no local changes. Divergence or dirty state fails without an automatic merge, reset, checkout, or stash, and retains the private result.

```text
ai-agent apply [repository] [--workspace <id>]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--workspace` | active workspace | Exact retained workspace result to apply |

Repeated application is safe; a no-change result completes without changing Git state.

## `ai-agent workspace`

List every retained workspace so IDs remain discoverable even after a source repository moves, or remove one explicitly. `apply --workspace <id>` accepts a moved checkout only when the recorded source path no longer exists and the supplied checkout has the same GitHub identity; this avoids guessing between two live clones.

```text
ai-agent workspace list
ai-agent workspace remove <workspace-id> [--force]
```

Listing keeps corrupt or older metadata visible as an `unreadable` row without hiding healthy entries, includes creation time, and orders healthy workspaces newest-first. Removal refuses a live launcher and reconciles any durably owned container before deleting its checkout, even when the source repository is unavailable. Without `--force`, removal also refuses uncommitted files or unapplied commits; forcing removal explicitly discards recoverable workspace content or unreadable metadata but never bypasses container quiescence.

## `ai-agent up` compatibility workflow

Bootstrap the whole local environment in one command: guided setup when config is missing, broker startup, readiness checks, optional Langfuse, devcontainer launch, agent login status, interactive shell.

```text
ai-agent up [--workspace <path>] [--project <path>] [--runtime podman|docker] [--build] [--observability=true|false] [-v]
```

| Flag | Default | Description |
|------|---------|-------------|
| `--workspace` | `.` | Host directory containing your repos, mounted at `/workspace` inside the generic container |
| `--project` | _(unset)_ | Path to a single project whose own `.devcontainer` is honored, with the broker overlay injected |
| `--runtime` | `podman` | Container runtime. Use `docker` only as an explicit opt-out. |
| `--build` | `false` | Force rebuild of the devcontainer image (no cache) |
| `--observability` | `true` | Start local Langfuse and authorize sanitized trace publication; set to `false` for local history only |
| `-v`, `--verbose` | `false` | Stream container build output to the terminal instead of the log |

Runs from any directory — the generic devcontainer definition ships inside the binary. This compatibility workflow mounts the explicitly supplied path and opens a shell; it never scans for or guesses a child repository. New governed work should use `ai-agent start` so sibling repositories and the human checkout are not mounted. If the runtime or the devcontainer CLI is missing, `ai-agent up` offers to install it; when Podman is selected but only Docker is present, it offers to install Podman or use Docker for that run.

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

Works inside the devcontainer. Host-native managed runs are rejected before brokered work begins. Verification behavior is described in [Quality Gates](quality-gates.md); what each run records is in [Observability](observability.md).

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

Each check carries an owner (which subsystem must fix it) and a severity: **required** checks block readiness, **advisory** checks surface as warnings. `--json` emits the same fields plus machine-readable `evidence`.

<!-- BEGIN generated: readiness-checks (regenerate with `make readiness-docs`) -->
| Check | Owner | Severity | What it verifies |
|-------|-------|----------|------------------|
| `binary-*` | host | required | Required binaries are installed and executable |
| `broker-configuration-recovery` | config | required | Governance configuration loads with owner-only access |
| `broker-identities` | config | required | The identities file exists and is valid |
| `broker-pem-files` | broker | required | Each agent PEM is a key the broker can load |
| `broker-pem-rotation` | broker | advisory | No agent PEM is past the rotation reminder age |
| `broker-policy` | config | required | The policy file exists and is valid |
| `broker-policy-providers` | config | required | Policy provider configs parse for every provider |
| `broker-provider-fields` | config | required | Required provider readiness fields are set |
| `broker-reachability` | broker | required | The broker answers a health check on its socket |
| `broker-socket` | broker | required | The broker socket exists and is a Unix domain socket |
| `broker-socket-env` | broker | required | The configured broker socket path is absolute and valid |
| `container-runtime` | runtime | required | The container runtime and devcontainer CLI are present |
| `container-workspace` | runtime | required | The workspace directory is set and mountable |
| `repo-remote` | config | required | The repository has an HTTPS origin remote (not SSH) |
| `runtime-dir` | host | required | XDG_RUNTIME_DIR exists and is a directory |
<!-- END generated: readiness-checks -->

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
