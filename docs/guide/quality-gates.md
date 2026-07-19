# Quality Gates

**Scope: making an agent prove its work, declaring the supported project operating model, and bounding what a run can spend.** Project manifest contracts, project devcontainer declarations, the verify-and-retry loop, and the token/output budgets that do not depend on agent cooperation. What a run *records* is [Observability](observability.md); flags are in [CLI Reference](cli-reference.md).

## Verification comes from one of two places

Post-task verification is declared either in the repository's project manifest at `.ai-agent/manifest.json`, or as an explicit `--verify-cmd` on `ai-agent run`. When both are present, `--verify-cmd` overrides the manifest for that run.

### Project manifest contracts

A repository declares its quality gates once, and every managed run executes them in order after the agent exits successfully:

```json
{
  "schema_version": "ai-agent-manifest/v2",
  "contracts": [
    {"name": "tests", "command": "make test"},
    {"name": "lint", "command": "make lint", "retry": "never"}
  ]
}
```

Each contract has a unique `name` (at most 64 characters), a `command` run via `sh -c` in the worktree root where the manifest lives (regardless of which `--repo` subdirectory the run starts from), and an optional `retry` policy:

- `agent` (default) — re-launch the agent when the contract fails
- `never` — fail the run immediately, without another agent attempt

Contracts run in declared order and stop at the first failure. An invalid manifest fails the run *before* a broker session is created. Per-contract outcomes, failure classes (`exit`, `signal`, `start_failed`), and attempt counts are recorded in run history and shown by `ai-agent runs show`.

### Agent allowlists and model defaults

The same manifest can govern which agents may work on the project:

```json
{
  "schema_version": "ai-agent-manifest/v2",
  "agents": {
    "allowed": ["claude", "codex"],
    "defaults": {"claude": {"model": "claude-sonnet-5"}}
  }
}
```

When `agents.allowed` is declared, `ai-agent run` treats each entry as a host agent identity: it refuses an unlisted `--agent` before a broker session is created, and refuses a launched command whose executable does not match that identity's configured `tool` (`claude-code` accepts the `claude` executable).

A per-agent model default overrides the host identity's configured model **for run attribution only** — it is recorded in run history and announced on stderr, but does not change the launched command or its environment. Agents absent from `defaults` keep the host-configured attribution model.

### Project operating model

Schema `ai-agent-manifest/v2` lets a repository declare the supported operating model that `ai-agent run` and `ai-agent up --project` enforce:

```json
{
  "schema_version": "ai-agent-manifest/v2",
  "agents": {"allowed": ["claude", "codex"]},
  "run_modes": ["managed_run", "project_devcontainer"],
  "resources": [{"uri": "langfuse:project:localdev"}],
  "caches": [{"name": "go-build", "target": "/workspace/.cache/go-build"}],
  "services": [{"name": "db"}],
  "ports": [{"number": 8080}],
  "resource_budgets": [{"name": "project-tokens", "metric": "tokens", "warn_at": 100000, "stop_at": 120000, "stop_policy": "stop_run"}]
}
```

Managed runs reject disallowed `run_modes`, invalid provider resources, policy-denied manifest resources, and unenforceable token budgets before broker session creation. Declared resources become broker session resources; no durable provider secret value is written into the manifest or projected into the workspace. `up --project` rejects disallowed project-devcontainer mode, adds declared cache volumes and forwarded ports, includes declared Compose services through the override config, rejects cache targets that overlap reserved ai-agent paths, and uses declared telemetry egress resources for the injected environment.

## Usage

```bash
# Run the manifest-declared contracts after the agent completes (no flags needed)
ai-agent run --agent claude --repo . -- claude

# Override the manifest for one run
ai-agent run --agent claude --repo . --verify-cmd "make verify" -- claude

# Custom verify command with 1 retry
ai-agent run --agent codex --repo . --verify-cmd "go test ./... && make lint" --max-retries 1 -- codex
```

## How the loop runs

1. The agent runs as a subprocess (rather than replacing the process via exec)
2. When the agent exits 0, each contract (or the `--verify-cmd` command) runs via `sh -c`
3. Passing output is hidden; failure output is capped at 60 lines and 256 KiB, with the full bounded output retained as local evidence under the standard retention caps
4. If every contract passes, the session is revoked and the launcher exits successfully
5. If a contract fails with `retry: agent` and retries remain, the agent is re-launched; a `retry: never` contract fails the run immediately
6. If the agent itself exits non-zero, the loop stops immediately — no verification
7. Once retries are exhausted, the session is revoked and an error is returned

The agent inherits the same scrubbed environment and memfd-based bind secret as a normal run. No security properties change.

## Home isolation

Managed runs execute with an isolated per-run `HOME` by default: an ephemeral directory that projects only agent login state (`.claude`, `.claude.json`, `.codex`, `.agents`), so personal `gh`, `git`, and SSH state is unreachable through home-relative paths or `..` traversal under projected directories — while agent logins keep persisting durably.

Codex install and scratch subtrees under `.codex/packages` and `.codex/tmp` stay in the real home and are not projected. Directory and file state is copied into the run home, then changed allowlisted state is committed back before the launcher reports a clean result. Run-created symlinks and top-level kind changes fail closed instead of being silently persisted.

Disable it for one run with `--isolate-home=false` if an agent needs other home-relative state. ADR 0014 records the trust limits.

## Budgets that do not depend on agent cooperation

These are on by default:

- Passing verification output is hidden; failed verification output is capped at 60 lines and 256 KiB.
- Automatic retries default to 2 and cannot exceed 10.
- `ai-agent check` caps each log at 10 MiB and retained evidence at 20 files or 20 MiB.
- Managed Claude and Codex runs capture provider-reported usage automatically, with no observability backend required.
- Project overlay tools are read-only.
- `--token-warn-at` and `--token-stop-at` bound a run's token spend. Both require native agent telemetry; a planned hard stop fails closed if the native relay cannot start.
- `resource_budgets` in a v2 manifest add project token budgets. Command-line token flags can add tighter limits for a run, but they do not remove manifest-declared budgets. Avoid naming a project budget `tokens` when using CLI token flags; that name is reserved for the CLI-derived budget in run evidence.

Small guidance files are installed when missing. They improve search and reporting habits but are not enforcement — existing user files are never overwritten, and a bootstrap failure warns without blocking the container.

## When to use this

- **Batch/headless agents** — Codex or similar agents that run a task and exit
- **CI-like workflows** — make `make verify` pass before a task counts as done
- **Prompt iteration** — agent changes code, tests run, agent gets another attempt on failure

Without manifest contracts or `--verify-cmd`, the launcher runs the agent once and streams its output normally.
