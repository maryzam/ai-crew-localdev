# Design: Task Isolation and Observable Development Workflow

## 1. Intent

`ai-agent` is a single-command CLI that launches hardened, isolated development sessions for AI coding agents (Claude Code, Codex, Gemini) operating on GitHub repositories through brokered GitHub App identities.

The long-term workflow is: **Plan -> Implement -> Validate -> Review -> Release**, executed in a semi-automated loop where AI agents do the bulk of implementation and review, and the human scopes work, writes invariant tests, and approves security-critical or architecture-significant changes.

Core properties, in priority order:

1. **Security is non-negotiable.** Agents run inside hardened containers. No secrets — signing keys, API keys, or any other credentials — enter the container except through the broker socket. All GitHub access goes through the broker. The same isolation principle applies to any future credential type (AI provider keys, cloud tokens, registry creds). A dedicated credential audit (follow-up to this doc) will validate that no obvious gaps exist for non-GitHub secrets.
2. **Observability is non-negotiable and non-opt-out.** Every session emits structured telemetry to Langfuse and git-notes. Langfuse is the enterprise-grade observability backend (chosen deliberately for skill-building, not just convenience). Telemetry data must persist independently of any single container or service shutdown — see Langfuse persistence in section 3.
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
| S6 | No secrets of any kind (GitHub, AI provider, cloud, registry) are passed to containers except through the broker socket or explicitly mounted read-only config | Credential audit (follow-up); invariant test scans container env for known secret patterns |

### Observability

| ID | Invariant | Enforcement |
|----|-----------|-------------|
| O1 | Every `Launch()` call emits a `session.started` event with `session_id`, `agent_name`, `repo` | Invariant test: mock telemetry interface, assert call on every launch path |
| O2 | Every session exit emits `session.ended` with exit code and verify-loop result | Supervisor model (see section 3): agent always runs as subprocess, never `syscall.Exec`. Supervisor regains control on exit and emits telemetry |
| O3 | Git notes are written on session start/end regardless of Langfuse availability | Invariant test: Langfuse unreachable, assert git note exists |
| O4 | `session_id` follows `{repo_short_name}#{issue_number}` convention | Schema validation in telemetry emit path |
| O5 | `metadata.repo` is `{github_org}/{repo_name}` on all Langfuse traces | Same |

### Task isolation

| ID | Invariant | Enforcement |
|----|-----------|-------------|
| T1 | Each task is backed by a `git clone --shared` of the main repo, not a worktree | `ai-agent task` implementation; tested by asserting `.git` is a directory (not a file) |
| T2 | The main repo's object store is mounted read-only inside the task container | Container mount specification; invariant test attempts write to objects path, expects failure |
| T3 | New objects created by the agent are written to the task clone's own `.git/objects/` | Git alternates mechanism; no special code needed |
| T4 | `ai-agent up --task N` requires a valid GitHub issue number | CLI validation via broker: `resolve_issue` broker method uses app auth to fetch issue metadata. No host-side `gh` auth required |
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

### Supervisor model (replacing `syscall.Exec`)

The current default launcher path ends in `syscall.Exec` (`internal/launcher/launcher.go:140`), which replaces the process entirely. The parent never regains control, making it impossible to emit `session.ended` telemetry or write end-of-session git notes.

**Decision:** always use subprocess mode. The launcher runs the agent as a child process (as the `--verify-cmd` path already does), wraps it in a thin supervisor that:

1. Emits `session.started` before agent launch
2. Waits for agent exit
3. Emits `session.ended` with exit code, duration, and verify-loop result
4. Writes git notes
5. Cleans up session (revoke + remove session file)

This changes `launcher.Launch()` from a one-way `Exec` to a supervised subprocess on all paths. The verify-and-retry loop (`launchWithVerify`) already implements steps 2-5; the change is removing the `syscall.Exec` branch and making subprocess mode the only mode.

### Bare mirror as object store

Task clones use `git clone --shared`, which links to a source repo's object store via the git `alternates` mechanism. Rather than cloning from the developer's primary checkout (which couples task clones to work-in-progress state and creates GC risk), the tool manages a **bare mirror** per repo.

#### Mirror layout

```
$XDG_DATA_HOME/ai-agent/mirrors/{org}/{repo}.git     bare mirror (managed by ai-agent)
$XDG_DATA_HOME/ai-agent/tasks/{org}/{repo}/           task clones (shared from mirror)
```

**Mirror lifecycle:**
- **Created** by `ai-agent setup` or lazily on first `ai-agent up --task N` for a repo
- **Updated** by `git fetch origin` inside the mirror. Enforced at two points:
  1. **Pre-task:** `ai-agent up --task N` fetches the mirror before cloning
  2. **Pre-push hook:** a git hook in each task clone runs `git -C $MIRROR fetch origin` before pushing, ensuring the mirror stays current with remote state
- **GC-safe:** `gc.auto = 0` on the mirror prevents automatic pruning. Manual GC is gated behind `ai-agent mirror gc` which checks for active task clones first

**Why a bare mirror:**
- Decouples from user's working checkout — no risk of corrupting in-progress work
- Provides a stable anchor for git notes (notes are written to the mirror, not to disposable task clones)
- Object store path is deterministic (`$XDG_DATA_HOME/...`), making container mount computation reliable
- Fetch state is managed centrally — no "which checkout did I clone from?" ambiguity

### What changes: task-scoped shared clones

**Why not git worktrees:** Worktrees share `.git/` state bidirectionally via absolute paths. Inside a container, both the worktree path and the main repo's `.git/worktrees/<name>/` path must resolve identically. This requires mounting the main repo's `.git/` read-write (breaking cross-task isolation) and triggers an open devcontainer CLI bug ([devcontainers/cli#796](https://github.com/devcontainers/cli/issues/796)). See `ANALYSIS.md` or the PR #41 review for the full comparison.

**Shared clones** (`git clone --shared`) create a real `.git/` directory per task. The only shared resource is the object store, linked read-only via git's `alternates` mechanism. Each clone has independent refs, HEAD, index, and config. Clones are created from the bare mirror (see above), not from the developer's checkout.

#### Task directory layout

```
$XDG_DATA_HOME/ai-agent/
  ├── mirrors/
  │   └── maryzam/ai-crew-localdev.git      bare mirror (object store source)
  └── tasks/
      └── maryzam/ai-crew-localdev/
          ├── 43-wire-langfuse/              shared clone, branch task/43-wire-langfuse
          │   ├── .git/                      real directory (independent refs, index, HEAD)
          │   │   └── objects/info/alternates → mirrors/.../objects (read-only)
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
    # Mirror object store — shared, READ-ONLY (alternates target)
    --mount type=bind,src=$MIRROR/objects,dst=$MIRROR/objects,ro \
    # Broker socket
    --mount type=bind,src=$XDG_RUNTIME_DIR/ai-agent,dst=/run/ai-agent,type=bind \
    # Standard hardening (existing devcontainer.json runArgs)
    --cap-drop=ALL --security-opt=no-new-privileges --read-only \
    --tmpfs=/tmp:rw,noexec,nosuid,size=512m \
    --tmpfs=/home/dev:rw,nosuid,size=1g \
    --userns=keep-id \
    --label ai-agent.task=maryzam/ai-crew-localdev#43
```

The clone's `.git/objects/info/alternates` file references the absolute path of the bare mirror's objects directory (`$MIRROR/objects` — bare repos have no `.git/` subdirectory). This resolves inside the container because the objects directory is mounted at the same host path.

New objects (from agent commits) go to `/workspace/.git/objects/` (the clone's own store). The mirror's objects are never written to from inside a container.

#### Issue resolution via broker

The `ai-agent up --task N` flow must validate the issue number and fetch metadata (title, labels) to derive the task slug. This cannot use host-side `gh` auth — the trust model puts all GitHub API access behind the broker.

**Design:** Add a `resolve_issue` method to the broker API (`internal/broker/api.go`). The broker already holds GitHub App credentials and mints tokens; resolving an issue is a read-only API call using the same auth path. The CLI sends `{method: "resolve_issue", body: {repo: "owner/repo", issue: N}}` and receives `{title, number, state, labels}`.

This keeps host-side `gh` auth entirely out of the `ai-agent up` path. The broker validates that the requesting identity has read access to the repo per the existing policy model.

#### Devcontainer configuration for task-scoped launches

The current `devcontainer.json` hard-codes `workspaceMount` to `${localEnv:AI_AGENT_WORKSPACE}` (line 31). This mount is a single bind of the user's repos directory. For task-scoped launches, each container needs a different workspace mount (the task clone) plus the read-only mirror objects mount.

Passing `--workspace-folder $TASK_DIR` to the `devcontainer` CLI is not sufficient — the hard-coded `workspaceMount` takes precedence.

**Design:** PR 3 must generate a per-task devcontainer config. Options:
- **(a)** Set `AI_AGENT_WORKSPACE` env var to the task clone path before invoking `devcontainer up`. The existing `${localEnv:AI_AGENT_WORKSPACE}` interpolation in `devcontainer.json` picks it up. The additional objects mount is passed via `--mount` CLI flag.
- **(b)** Generate a temporary `devcontainer.json` per task that overrides `workspaceMount` and `mounts`.

Recommended: **(a)** — minimal change, reuses existing env-var interpolation, avoids config file generation.

#### Task lifecycle (`ai-agent up --task N`)

```
1. Resolve issue via broker: resolve_issue {repo, N} → {title, number, state}
2. Derive slug from issue title (sanitize, clamp to 60 chars)
3. Ensure mirror exists:
     if $MIRROR does not exist:
       git clone --bare https://github.com/{org}/{repo}.git $MIRROR
       git -C $MIRROR config gc.auto 0
     else:
       git -C $MIRROR fetch origin
4. Create task clone:
     DEFAULT_BRANCH=$(git -C $MIRROR symbolic-ref refs/remotes/origin/HEAD | sed 's|refs/remotes/origin/||')
     git clone --shared --no-checkout $MIRROR $TASK_DIR/{N}-{slug}
     cd $TASK_DIR/{N}-{slug}
     git remote set-url origin https://github.com/{org}/{repo}.git
     git checkout -b task/{N}-{slug} origin/$DEFAULT_BRANCH
     install pre-push hook: git -C $MIRROR fetch origin
5. Emit: git-notes session.prepared (to mirror) + Langfuse trace (if reachable)
6. Start devcontainer:
     AI_AGENT_WORKSPACE=$TASK_DIR/{N}-{slug} devcontainer up \
       --config $DEVCONTAINER_CONFIG \
       --workspace-folder $TASK_DIR/{N}-{slug} \
       --mount "src=$MIRROR/objects,dst=$MIRROR/objects,ro"
7. Inside container (supervisor mode):
     ai-agent run --agent {agent} --repo /workspace -- {agent-cmd}
     → emits session.started
     → runs agent as subprocess
     → on exit: emits session.ended, writes git notes, revokes session
```

#### Repo onboarding: unconfigured repos

This design requires repos to have a `.devcontainer/` directory for the container build. Two cases:

**Case 1: repo has `.devcontainer/` (ai-agent-managed repos).** The task clone contains the devcontainer config. `devcontainer up` uses it directly. This is the primary supported path.

**Case 2: repo does not have `.devcontainer/` (arbitrary GitHub repos).** The CLI falls back to the ai-agent default devcontainer config shipped with the tool (at `$XDG_DATA_HOME/ai-agent/default-devcontainer/`). This default config provides the same hardening (cap-drop, read-only root, tmpfs home) and pre-installs the AI CLI tools, but uses a generic base image. The CLI detects the missing `.devcontainer/` and uses `--config $DEFAULT_DEVCONTAINER` instead.

`ai-agent doctor` should warn when a repo lacks `.devcontainer/` and offer to scaffold one from the default template via `ai-agent setup --init-devcontainer`.

### Langfuse persistence

The Langfuse stack (`contrib/langfuse/docker-compose.yml`) currently uses **named Docker volumes** for Postgres, ClickHouse, and MinIO:

```yaml
volumes:
  langfuse-postgres:     # /var/lib/postgresql/data
  langfuse-clickhouse:   # /var/lib/clickhouse
  langfuse-minio:        # MinIO object storage
```

**Current state assessment:** Named volumes survive `docker compose down` and container restarts. They are destroyed by `docker compose down -v` or `docker volume prune` — both common operations that a developer might run during unrelated cleanup.

**Recommendation: mark volumes as `external: true` + managed creation.**

1. Change compose volumes to `external: true`. This prevents `docker compose down -v` from removing them.
2. `ai-agent up --langfuse` creates the volumes explicitly before starting compose (`docker volume create langfuse-postgres` etc.).
3. Add `make langfuse-backup` target that runs `pg_dump` and tars ClickHouse data to `$XDG_DATA_HOME/ai-agent/langfuse-backup/` for point-in-time recovery.

**Why not bind mounts:** Database containers (Postgres, ClickHouse) run as internal users with specific UIDs. Bind mounts require matching host-side ownership, which breaks across Docker/Podman and rootless configurations. Named volumes handle UID mapping correctly regardless of runtime. External volumes give the same durability guarantee without the permissions complexity.

### Git notes durability

Git notes record session events as the durable fallback (invariant O3). Since task clones are disposable (`task close` deletes them), notes cannot live in the clone.

**Decision:** notes are written to the **bare mirror** at `refs/notes/agent-log/{repo_short_name}`. The mirror is a long-lived managed artifact that persists across task lifecycles. Notes are pushed to the GitHub remote periodically (on `task close` and via the pre-push hook) so they survive even mirror re-creation.

**Commit attribution:** task-local commits live in the disposable shared clone, not in the mirror. The supervisor must resolve the task clone's HEAD commit SHA *before* cleanup, then fetch that commit into the mirror and attach the note to it:

```
# After agent exits, before clone deletion:
TASK_HEAD=$(git -C $TASK_DIR/{N}-{slug} rev-parse HEAD)

# Fetch task objects into mirror so the commit is reachable:
git -C $MIRROR fetch $TASK_DIR/{N}-{slug} task/{N}-{slug}

# Write note against the actual task commit:
git -C $MIRROR notes --ref=refs/notes/agent-log/localdev add -m '{...}' $TASK_HEAD

# Push notes to remote:
git -C $MIRROR push origin refs/notes/agent-log/localdev
```

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
| 1 | `feat: add telemetry interface and git-notes audit trail` | Define `Telemetry` interface (emit session events). Implement git-notes backend (writes to bare mirror). Add invariant test asserting every `Launch()` path calls emit. Wire Langfuse OTLP as best-effort second backend. | -- | `internal/telemetry/`, `internal/launcher/launcher.go` |
| 2 | `refactor: replace syscall.Exec with supervisor subprocess` | Remove the `syscall.Exec` branch in `launcher.Launch()`. All paths use subprocess mode (as `launchWithVerify` already does). Supervisor emits `session.started`/`session.ended` and writes git notes on every exit path. Prerequisite for O2/O3. | 1 | `internal/launcher/launcher.go` |
| 3 | `feat: add bare mirror management and resolve_issue broker method` | Add `resolve_issue` to broker API (fetches issue metadata via app auth). Add mirror create/fetch logic. Add `MirrorDir()`, `TaskDir()` to `internal/config/paths.go`. Set `gc.auto=0` on mirrors. | -- | `internal/broker/api.go`, `internal/mirror/`, `internal/config/paths.go` |
| 4 | `feat: ai-agent up --task N (shared clone + devcontainer)` | `--task N` flag on `ai-agent up`: resolve issue via broker, create shared clone from mirror, set remote, install pre-push hook, set `AI_AGENT_WORKSPACE` to task clone, start devcontainer with `--mount` for read-only mirror objects, add container label. Invariant test for T2 (objects mount read-only). | 2, 3 | `internal/cli/up.go`, `internal/task/` |
| 5 | `feat: ai-agent task list and task close` | `ai-agent task list`: scan `$XDG_DATA_HOME/ai-agent/tasks/`, show issue, branch, container status. `ai-agent task close --issue N`: stop container, push notes to remote, optionally push branch, `rm -rf` clone. | 4 | `internal/cli/task.go`, `internal/task/` |
| 6 | `feat: unconfigured repo fallback devcontainer` | Ship default devcontainer config at `$XDG_DATA_HOME/ai-agent/default-devcontainer/`. `ai-agent up --task N` detects missing `.devcontainer/` and falls back. `ai-agent doctor` warns. `ai-agent setup --init-devcontainer` scaffolds. | 4 | `internal/cli/up.go`, `internal/cli/setup.go`, default config files |
| 7 | `fix: mark Langfuse volumes as external, add backup target` | Change compose volumes to `external: true`. `ai-agent up --langfuse` creates volumes before compose up. Add `make langfuse-backup` for pg_dump + ClickHouse tar. | -- | `contrib/langfuse/docker-compose.yml`, `internal/cli/up.go`, `Makefile` |
| 8 | `fix: add rebase-and-build CI check for open PRs` | GitHub Action that rebuilds open PRs when `main` advances, catching stale-branch compile breaks like PR #41. | -- | `.github/workflows/ci.yml` |

PRs 1, 3, 7, and 8 are independent and can be developed in parallel. PR 2 depends on 1. PR 4 depends on 2 and 3. PR 5 depends on 4. PR 6 depends on 4.

### Closed PRs (context)

- **PR #40** (ANALYSIS.md): closed. Jules analysis doc with two factual corrections identified. Findings incorporated into this design.
- **PR #41** (task worktree launcher): closed. Superseded by this design — shared clones replace worktrees, supervisor replaces `syscall.Exec`, bare mirror replaces direct checkout cloning.

### Out of scope (future)

- `--task` as mandatory (tighten later when ad-hoc shell is no longer needed)
- Multi-project meta-process / cross-repo task dashboard
- Remote approval flow (Telegram bot for T2/T3 PR approvals)
- Lightweight local telemetry viewer replacing heavy Langfuse stack
- Removing `devcontainer` CLI dependency (native Go container orchestration)
- macOS / non-apt Linux support
- Credential audit: deep review of all secret types (AI provider API keys, cloud tokens, registry creds) to validate that the broker isolation model covers them or that explicit gaps are documented
