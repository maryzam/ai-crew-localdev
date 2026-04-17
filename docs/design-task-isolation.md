# Design: Task Isolation and Observable Development Workflow

## 1. Intent

`ai-agent` is a single-command CLI that launches hardened, isolated development sessions for AI coding agents (Claude Code, Codex, Gemini) operating on GitHub repositories through brokered GitHub App identities.

The long-term workflow is: **Plan -> Implement -> Validate -> Review -> Release**, executed in a semi-automated loop where AI agents do the bulk of implementation and review, and the human scopes work, writes invariant tests, and approves security-critical changes.

Core properties, in priority order:

1. **Security is non-negotiable.** Agents run inside hardened containers. Signing keys never enter the container. All GitHub access goes through the broker. No ambient credentials.
2. **Observability is non-negotiable and non-opt-out.** Every session emits structured telemetry to Langfuse and git-notes. Logs persist independently of any single container or service.
3. **GitHub is the system of record.** Issues define work. PRs gate changes. Actions enforce guardrails. Hooks automate review.
4. **One command to start working.** `ai-agent up --task <issue>` is the primary entry point. It must be as easy as (or easier than) running native coding agents.
5. **Task isolation via containers.** Each GitHub issue gets one git clone and one container. Multiple issues run in parallel without interference.

## 2. Invariants

These are testable assertions. Each must have a corresponding `_invariants_test.go` or CI check before the feature that depends on it ships.

### Security

| ID | Invariant | Enforcement |
|----|-----------|-------------|
| S1 | Agent processes never see PEM signing keys | Broker holds keys; container receives only a Unix socket (`AI_AGENT_AUTH_SOCK`) |
| S2 | Agents run inside a hardened container: `--cap-drop=ALL`, `--security-opt=no-new-privileges`, `--read-only` rootfs, tmpfs `/home/dev` | `devcontainer.json` runArgs; invariant test asserts `/proc/self/status` values |
| S3 | `git` and `gh` are fail-closed shims; without the broker socket they do nothing | `scrub_invariants_test.go`, `memfd_invariants_test.go` |
| S4 | Each task container sees only its own working tree + read-only shared objects. No cross-task write access | Container mount specification (see section 3) |
| S5 | No host ambient credentials leak into the container (`~/.ssh`, `~/.aws`, host `gh` tokens, `.netrc`) | `ScrubEnv` + read-only rootfs + tmpfs home; invariant test asserts absence of known credential paths |

### Observability

| ID | Invariant | Enforcement |
|----|-----------|-------------|
| O1 | Every `Launch()` call emits a `session.started` event with `session_id`, `agent_name`, `repo` | Invariant test: mock telemetry interface, assert call on every launch path |
| O2 | Every session exit emits `session.ended` with exit code and verify-loop result | Same |
| O3 | Git notes are written on session start/end regardless of Langfuse availability | Invariant test: Langfuse unreachable, assert git note exists |
| O4 | `session_id` follows `{repo_short_name}#{issue_number}` convention | Schema validation in telemetry emit path |
| O5 | `metadata.repo` is `{github_org}/{repo_name}` on all Langfuse traces | Same |

### Task isolation

| ID | Invariant | Enforcement |
|----|-----------|-------------|
| T1 | Each task is backed by a `git clone --shared` of the main repo, not a worktree | `ai-agent task` implementation; tested by asserting `.git` is a directory (not a file) |
| T2 | The main repo's object store is mounted read-only inside the task container | Container mount specification; invariant test attempts write to objects path, expects failure |
| T3 | New objects created by the agent are written to the task clone's own `.git/objects/` | Git alternates mechanism; no special code needed |
| T4 | `ai-agent up --task N` requires a valid GitHub issue number | CLI validation; `gh issue view N` must succeed |
| T5 | One container per task. Container is labelled `ai-agent.task={org}/{repo}#{issue}` | devcontainer launch args; `ai-agent task list` reads these labels |

## 3. Architecture

### What exists today (on `main`)

```
Host
 ├── ai-agent-broker          daemon; holds PEM keys; serves Unix socket
 │     └── broker.sock        at $XDG_RUNTIME_DIR/ai-agent/broker.sock
 ├── ai-agent up              starts broker, runs doctor, launches devcontainer, opens shell
 ├── ai-agent run              (inside container) creates session, scrubs env, execs agent
 ├── ai-agent doctor           readiness checks
 ├── ai-agent setup            interactive GitHub App + identity + policy bootstrap
 ├── devcontainer              hardened: cap-drop, no-new-privs, read-only root, tmpfs home
 │     ├── ai-agent-credential-helper    fail-closed git credential shim
 │     ├── ai-agent-gh                   fail-closed gh wrapper
 │     └── claude / codex / gemini       pre-installed AI CLIs
 └── contrib/langfuse/         self-hosted Langfuse v3 stack (7 containers, docker-compose)
```

Key contracts defined in code:
- Broker API: `internal/broker/api.go` (Unix socket JSON-RPC: `mint_token`, `create_session`, `revoke_session`)
- Audit events: `internal/broker/audit.go` (`session.created`, `session.revoked`, `token.minted`, etc.)
- Session model: `internal/broker/session.go`
- Launcher: `internal/launcher/launcher.go` (resolve repo, create session, memfd bind secret, scrub env, exec agent)
- Config paths: `internal/config/paths.go` (XDG-compliant: `ConfigDir()`, `RuntimeDir()`, `DefaultSocketPath()`)
- Policy: `internal/policy/` (ValidateResult with `.Errors` and `.Warnings`)
- Identity: `internal/identity/` (`Load(path string)` takes file path)

Existing invariant tests: `scrub_invariants_test.go`, `memfd_invariants_test.go`, `session_invariants_test.go`.

### What changes: task-scoped shared clones

**Why not git worktrees:** Worktrees share `.git/` state bidirectionally via absolute paths. Inside a container, both the worktree path and the main repo's `.git/worktrees/<name>/` path must resolve identically. This requires mounting the main repo's `.git/` read-write (breaking cross-task isolation) and triggers an open devcontainer CLI bug ([devcontainers/cli#796](https://github.com/devcontainers/cli/issues/796)). See `ANALYSIS.md` or the PR #41 review for the full comparison.

**Shared clones** (`git clone --shared`) create a real `.git/` directory per task. The only shared resource is the object store, linked read-only via git's `alternates` mechanism. Each clone has independent refs, HEAD, index, and config.

#### Task directory layout

```
$XDG_DATA_HOME/ai-agent/tasks/{org}/{repo}/
  ├── 43-wire-langfuse/              shared clone, branch task/43-wire-langfuse
  │   ├── .git/                      real directory (independent refs, index, HEAD)
  │   │   └── objects/info/alternates → $MAIN_REPO/.git/objects (read-only link)
  │   ├── .devcontainer/             present in clone (full working tree)
  │   ├── internal/
  │   └── ...
  └── 44-refactor-broker/            another task, fully independent
```

#### Container mount architecture

```
ai-agent up --task 43:

  podman run \
    # Task working directory — agent writes code here
    --mount type=bind,src=$TASK_DIR/43-wire-langfuse,dst=/workspace \
    # Main repo object store — shared, READ-ONLY (alternates target)
    --mount type=bind,src=$MAIN_REPO/.git/objects,dst=$MAIN_REPO/.git/objects,ro \
    # Broker socket
    --mount type=bind,src=$XDG_RUNTIME_DIR/ai-agent,dst=/run/ai-agent,type=bind \
    # Standard hardening (existing devcontainer.json runArgs)
    --cap-drop=ALL --security-opt=no-new-privileges --read-only \
    --tmpfs=/tmp:rw,noexec,nosuid,size=512m \
    --tmpfs=/home/dev:rw,nosuid,size=1g \
    --userns=keep-id \
    --label ai-agent.task=maryzam/ai-crew-localdev#43
```

The clone's `.git/objects/info/alternates` file references the host-absolute path `$MAIN_REPO/.git/objects`. This resolves inside the container because the objects directory is mounted at the same host path.

New objects (from agent commits) go to `/workspace/.git/objects/` (the clone's own store). The main repo's objects are never written to from inside a container.

#### Task lifecycle (`ai-agent up --task N`)

```
1. Validate: gh issue view N --json title,number
2. Derive slug from issue title (sanitize, clamp to 60 chars)
3. Create clone:
     git clone --shared --no-checkout $MAIN_REPO $TASK_DIR/{N}-{slug}
     cd $TASK_DIR/{N}-{slug}
     git remote set-url origin https://github.com/{org}/{repo}.git
     git checkout -b task/{N}-{slug} origin/main
4. Emit: git-notes session.prepared + Langfuse trace (if reachable)
5. Start devcontainer:
     devcontainer up \
       --config $MAIN_REPO/.devcontainer/devcontainer.json \
       --workspace-folder $TASK_DIR/{N}-{slug} \
       --mount "src=$MAIN_REPO/.git/objects,dst=$MAIN_REPO/.git/objects,ro"
6. Inside container: ai-agent run --agent {agent} --repo /workspace -- {agent-cmd}
```

**Main repo GC safety:** Set `gc.pruneExpire = never` on the main repo during `ai-agent setup`. This prevents object pruning from breaking task clones that reference shared objects.

### Observability schema

Defined in `docs/dev-workflow-architecture.md:91-105`. Implementation must match exactly:

| Field | Convention | Example |
|-------|-----------|---------|
| `session_id` | `{repo_short_name}#{issue_number}` | `localdev#43` |
| `metadata.repo` | `{github_org}/{repo_name}` | `maryzam/ai-crew-localdev` |
| OTel `service.name` | repo short name | `localdev` |
| Git notes ref | `refs/notes/agent-log/{repo_short_name}` | `refs/notes/agent-log/localdev` |

The telemetry interface should be injected into `launcher.Launch()` so it can be mocked in tests. Git-notes writes are the durable fallback; Langfuse emission is best-effort (fail-open for telemetry, never fail-open for auth).

Existing broker audit types in `internal/broker/audit.go` (`AuditEvent`, `AuditLogger`) are the natural extension point for session-level telemetry.

## 4. Gap closure plan

Ordered by dependency. Each row is one PR. All PRs target `main`.

| Order | PR title | What it does | Depends on | Key files |
|-------|----------|-------------|------------|-----------|
| 1 | `feat: add telemetry interface and git-notes audit trail` | Define `Telemetry` interface (emit session events). Implement git-notes backend. Add invariant test asserting every `Launch()` path calls emit. Wire Langfuse OTLP as best-effort second backend. | -- | `internal/telemetry/`, `internal/launcher/launcher.go` |
| 2 | `feat: add ai-agent task prepare (shared clone from issue)` | `ai-agent up --task N` flag: validate issue via `gh`, create shared clone, set remote, create branch, emit `session.prepared`. Add `TaskDir()` to `internal/config/paths.go`. Set `gc.pruneExpire=never` on main repo if unset. | -- | `internal/cli/up.go`, `internal/task/`, `internal/config/paths.go` |
| 3 | `feat: launch devcontainer per task with scoped mounts` | When `--task N` is set, `ai-agent up` starts the devcontainer with the task clone as workspace and the main repo objects mounted read-only. Add container label `ai-agent.task`. Invariant test for T2 (objects mount is read-only). | 2 | `internal/cli/up.go`, `.devcontainer/devcontainer.json` (may need dynamic mount support) |
| 4 | `feat: ai-agent task list and task close` | `ai-agent task list`: scan `$XDG_DATA_HOME/ai-agent/tasks/`, show issue, branch, container status. `ai-agent task close --issue N`: stop container, optionally push branch, `rm -rf` clone, clean up local branch. | 2,3 | `internal/cli/up.go` (subcommands or flags), `internal/task/` |
| 5 | `fix: add rebase-and-build CI check for open PRs` | GitHub Action that rebuilds open PRs when `main` advances, catching stale-branch compile breaks like PR #41. | -- | `.github/workflows/ci.yml` |

PRs 1 and 2 are independent and can be developed in parallel. PR 5 is independent of everything.

### Existing open PRs

- **PR #40** (ANALYSIS.md): docs-only, pending Jules update on two factual corrections you already posted. Can merge independently.
- **PR #41** (task worktree launcher): superseded by this design. Recommend closing with a reference to this doc. The shared-clone approach in PR 2-3 replaces it.

### Out of scope (future)

- `--task` as mandatory (tighten later when ad-hoc shell is no longer needed)
- Multi-project meta-process / cross-repo task dashboard
- Remote approval flow (Telegram bot for T2/T3 PR approvals)
- Lightweight local telemetry viewer replacing heavy Langfuse stack
- Removing `devcontainer` CLI dependency (native Go container orchestration)
- macOS / non-apt Linux support
