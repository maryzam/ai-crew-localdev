# ADR 0019: Governed Repository Sessions

## Status

Accepted

This change implements the secure default vertical slice: `start`, `start --new`, active or ID-selected `apply`, `workspace list`, guarded `workspace remove`, identity-bound private checkouts, exclusive persisted run leases with stale-run reconciliation, result checkpointing after container quiescence, fast-forward-only integration, bounded operations, and atomic size-bounded transition evidence. The broader lifecycle commands and states described below (`shell`, `status`, `stop`, `preparing`, `ready`, and `discarded`) remain the accepted target architecture and are not exposed as available commands by this change.

## Context

The generic `ai-agent up` workflow bind-mounts a caller-selected host directory and opens a shell before the operator constructs a separate `ai-agent run` command. Repository-aware landing attempted to make a parent-directory mount more convenient by discovering a sole child repository and changing the shell directory. That heuristic made the host filesystem stand in for the container filesystem and coupled repository discovery, worktree portability, terminal rendering, shell construction, agent selection, and re-entry behavior. It also left the agent and the operator sharing one writable checkout, so either side could observe or overwrite changes made by the other while a session was active.

The product-level intent is smaller than those mechanics: start one governed agent in one repository, preserve its work independently, and integrate the result only when the operator asks. The shortest command must also be the narrowest authority path. A parent workspace, sibling scan, generated `devcontainer exec` command, host credential mount, remote clone on every start, or shared writable Git metadata must not be prerequisites for the default flow.

## Decision

### User contract

The primary command is `ai-agent start [repository]`. With no repository argument it resolves the Git worktree containing the current directory; with an argument it resolves the worktree containing that path. Resolution never scans children or siblings and never guesses among repositories. `ai-agent start` starts or resumes the active governed workspace session for that source checkout, selects the configured agent, launches or reuses the managed container, and runs that agent in `/workspace`. `--new` creates another session from the source checkout's current committed state. Arguments after `--` are passed to the configured tool; the operator does not repeat the executable name.

The target lifecycle adds `ai-agent shell` for an exceptional governed shell in the same private workspace, plus richer `status`, `stop`, and durable discard states. Until those commands land, re-entry uses `start`, `workspace list` discovers retained IDs, `workspace remove` performs guarded physical reclamation, and no generated `devcontainer exec` command is part of the user contract.

Agent selection is deterministic. An explicit `--agent` wins; otherwise a sole eligible configured agent wins. If several eligible agents remain, the invocation fails with the exact required `--agent` remediation. A future durable configured default or interactive picker may remove that prompt without changing the non-interactive contract. Repository manifests may narrow eligible identities but cannot grant an identity or capability absent from host governance. The selected identity's tool is resolved through compiled agent capabilities, and commands remain structured argument arrays through execution.

### Repository and workspace boundary

The repository selected by the operator is the source checkout. It supplies canonical repository identity, canonical source path, named source branch, exact base commit, and committed content; it is never the writable container workspace. The default path requires an existing commit, a named branch, no merge, rebase, cherry-pick, revert, or bisect operation, and no staged, unstaged, conflicted, or untracked changes. Ignored files are not part of the session. A refusal happens before a workspace directory, container, broker session, or credential-bearing process is created and explains how to commit, stash, remove, or explicitly snapshot the work in a future supported flow. There is no implicit dirty-tree snapshot and no silent omission of local changes.

The host creates an owner-only, session-specific, self-contained Git checkout below the ai-agent data directory and mounts only that checkout at `/workspace`. The source checkout, its parent, sibling repositories, and external linked-worktree metadata are not mounted. The session has its own `.git` directory, index, configuration, refs, reflogs, and writable object store. It has no object alternates and does not share writable Git metadata or object hardlinks with the source. Symlinked source paths canonicalize to one source identity, while every derived session path is recomputed from validated identifiers below the managed data root rather than trusted from persisted path text.

Provisioning copies Git objects from the local source with local file transport, disables hardlinks, disables hooks and checkout filters, and checks out the previously captured base commit. Its Git environment removes `GIT_DIR`, `GIT_WORK_TREE`, `GIT_COMMON_DIR`, `GIT_OBJECT_DIRECTORY`, alternate-object variables, config injection variables, and credential variables; it uses an isolated home, disables system configuration, denies every transport except the explicitly enabled local file transport, and never contacts the configured origin. The resulting checkout is verified to have the recorded HEAD and tree, an internal common Git directory, no alternates, and no dependency on the source path before it becomes ready. Its `origin` is then set to the canonical credential-free HTTPS repository URL so later network operations can pass through the broker. The clone-generated repository configuration is retained as trusted metadata so Git-owned format declarations such as SHA-256 object format or reftable storage survive; only ai-agent-owned hook, monitor, and remote keys are set, and the trusted configuration is restored before host-side Git operations. Source hooks, filters, credential helpers, and repository-selection environment never execute during host-side planning or provisioning.

Gitlinks are refused until submodule materialization has its own no-network, self-contained contract. Checkout filters, including Git LFS smudge, remain disabled during provisioning; the session contains committed blob bytes rather than silently invoking network or host tools. A future local object cache may replace direct copying only if sessions remain self-contained or the cache is mounted read-only with explicit liveness, garbage-collection, integrity, and audit guarantees. It is not part of this decision's first implementation.

The generic managed container keeps `/workspace` as its fixed `workspaceFolder`. Its launch plan contains the private checkout as the only source-code bind mount. The broker socket remains the only host credential boundary, the root filesystem remains read-only, capabilities remain dropped, `no-new-privileges` remains enabled, and neither a container runtime socket nor the operator's Git, SSH, GitHub CLI, home, or parent workspace is mounted. Agent login state is projected according to the isolated-home decision and is scoped by configured agent identity rather than used as a general host home. A governed container is resolved by its exact devcontainer label and force-removed after the agent process exits; checkpointing proceeds only after this quiescence proof, and its per-workspace build context is then reclaimed.

### Durable session model

A workspace session ID is distinct from an ephemeral broker session ID and managed-run ID. The owner-only durable workspace record contains a schema version, session ID, repository key, canonical source root, repository slug and canonical HTTPS remote, source branch, base commit and tree, private checkout identity, lifecycle state, result commit and tree when present, plan digest, creation and transition times, and the last reason code. It never contains credentials. Record publication uses a synced temporary file, atomic rename, and directory sync. Creation and transitions are serialized by a per-source-repository owner-only lock with a bounded acquisition deadline.

The complete target lifecycle is:

```text
absent -> preparing -> ready -> running -> result-ready -> applying -> applied
                         ^          |
                         |----------|
```

| From | To | Only when |
|------|----|-----------|
| absent | preparing | A validated plan, lock, and durable creation intent exist. |
| preparing | ready | The checkout and ready evidence are verified and durable. |
| preparing | failed | Provisioning or reconciliation cannot establish the ready invariant. |
| ready | running | A durable running lease exists and launch is about to begin. |
| result-ready | running | A durable running lease exists and the retained result remains recoverable while work resumes. |
| running | result-ready | The process is no longer live and finalization evidence, result commit, and result tree are durable. |
| running | failed | The process is no longer live but deterministic finalization or reconciliation failed. |
| result-ready | applying | All non-mutating apply preconditions and durable apply intent have succeeded. |
| applying | applied | The source HEAD, index, and worktree verify against the result and outcome evidence is durable. |
| applying | result-ready | Reconciliation proves the source remains clean at the base and no apply mutation took effect. |
| applying | failed | Reconciliation observes any state other than the verified base or verified result invariants. |
| ready, result-ready, failed, or applied | discarded | No process or apply transition is live and explicit discard intent is durable. |

No other transition is valid. `discarded` is a durable tombstone; physical pruning is a separate bounded operation that derives its target from the validated session ID and managed root.

`preparing` is persisted before checkout mutation. It becomes `ready` only after checkout verification and durable readiness evidence. `ready` and `result-ready` may enter `running`; a resumed `result-ready` session retains its last result until a later successful finalization supersedes it. A live running lease prevents a second launcher from entering the checkout. A stale running lease is reconciled from container, process, checkout, and Git facts before another transition. A partially prepared workspace is never inferred to be ready from directory existence alone.

Normal agent exit, agent failure, verification failure, and handled interruption finalize workspace changes only after the governed container is removed, then release the running lease. Failure to prove container removal records `failed` without checkpointing the still-writable checkout. Finalization requires the session history to remain descended from the recorded base, stages tracked and untracked workspace changes, creates an agent-attributed result commit when necessary, records a no-op result when the result tree equals the base tree, verifies the result, and persists `result-ready`. Unsupported filesystem entries, Git failures, ancestry rewrites, or evidence failures retain the private checkout and fail closed; they never discard agent work. `failed` and interrupted states require deterministic reconciliation or explicit discard, not silent replacement.

### Apply contract

`ai-agent apply` is deliberately narrower than an automatic merge. It first validates the session result without executing session hooks or trusting session configuration, then re-resolves the recorded source path and verifies the same repository identity, named branch, exact base HEAD, clean index and worktree, and absence of an in-progress Git operation. A precondition refusal does not modify the source repository, its object database, refs, index, or working tree. The session and result commit remain intact, and the diagnostic provides explicit merge, cherry-pick, patch, or new-session choices without performing one.

After the last preflight, apply records durable `applying` intent, transfers the exact result objects with a sanitized local-only Git environment, and performs only a fast-forward of the recorded source branch from the recorded base commit to the verified result commit. It never auto-merges a diverged source, checks out another branch, stashes human changes, resets, force-updates, or resolves conflicts. Git's own worktree overwrite protection remains enabled. The source HEAD, index, and tree are verified against the result before `applied` is persisted. A no-op result records `applied` without changing Git state, and a repeated apply is idempotent.

An apply interrupted after intent is reconciled before retry: source HEAD at the verified result with a matching clean tree becomes `applied`; source HEAD still at the base with a clean tree returns to `result-ready`; any other fact pattern becomes `failed` with the session retained. Unexpected I/O failure or an external process mutating the human checkout during the fast-forward cannot be made transactional across arbitrary editors and the filesystem. The implementation must not claim otherwise: it minimizes that window, relies on Git's index and ref locks, verifies the outcome, emits the observed state, and never runs an automatic reset as recovery.

### Budgets, evidence, and failure policy

Repository locking has a 10-second acquisition budget, source inspection and lifecycle listing have a 30-second command budget, local provisioning has a 5-minute command budget, finalization and apply each have a 2-minute command budget, container cleanup has a 30-second budget, and a workspace record is limited to 64 KiB. Transition evidence retains the newest 128 entries with an explicit dropped-sequence count and chain origin so the record cannot grow into permanent failure. These are named constants included in transition evidence. A timeout terminates the subprocess, records a stable reason code and elapsed duration, retains any session data already published, and fails closed. No unbounded scan or sequence of per-child Git probes is permitted.

Every material transition writes durable local evidence containing workspace session ID, repository slug, base and result commits when known, old and new state, plan digest, reason code, elapsed duration, budget, and outcome. Path details remain local and terminal rendering uses one terminal-safe encoder. Credentials, bind secrets, raw child environment, and unbounded child output are never evidence fields. Required intent evidence is durable before external mutation. If outcome evidence cannot be persisted after a mutation, the state remains transitional and the next invocation reconciles facts before proceeding; evidence is never silently dropped and governance paths fail closed.

Stable reason codes distinguish at least no repository, dirty source, unborn or detached source, in-progress Git operation, unsupported gitlink, invalid remote, lock timeout, inspection timeout, provision timeout, checkout verification failure, active lease, corrupt or unsafe state, result ancestry violation, apply source mismatch, apply branch mismatch, apply source moved, apply source dirty, apply timeout, and reconciliation failure. Human diagnostics may improve without changing automation semantics.

### Planning and package boundaries

The application layer owns start, resume, finalize, apply, stop, and discard orchestration through narrow ports. A pure immutable session plan owns resolved repository identity, selected agent, exact base, paths by identity, runtime mounts, budgets, and policy digests. A Git workspace adapter owns sanitized Git plumbing and verification. A durable store owns records, locks, atomic transitions, and recovery-safe path derivation. The container runtime owns provisioning and execution from a validated plan. CLI code owns interaction and terminal-safe presentation only. Broker policy, provider integration, telemetry transport, and managed-run execution remain separate.

Domain results are typed outcomes rather than `string, bool` pairs. Subprocesses receive `context.Context`, explicit argument arrays, a minimal constructed environment, bounded output capture, and injected runners in focused tests. No package passes a display command back into execution, and no runtime package applies terminal policy to repository names.

### Migration

`ai-agent start` and the private workspace lifecycle land before changing compatibility commands. During one documented compatibility period, `ai-agent up --workspace <repository>` routes to `ai-agent shell <repository>` and warns that parent-workspace mounting, sole-child discovery, and `--project` execution are deprecated. `ai-agent run` remains an internal and compatibility entrypoint inside an already governed container but is not printed as the next user action. Existing explicit advanced flags continue only where they preserve the one-repository mount and broker boundary.

Sole-child repository discovery, `LandingRepo`, shell-script `cd`, generated re-entry commands, default parent-workspace mounting, and implicit execution of repository-owned devcontainer configuration are removed from the supported path. After the compatibility period, `up` and manually constructed `run` workflows are removed or retained only as hidden internal interfaces with equivalent policy enforcement. Migration never silently changes a parent-directory invocation into authority over one guessed child.

## Verification contract

Focused automated checks use real temporary Git repositories for Git semantics and fakes only at application and runtime ports. They prove that nested paths resolve their containing worktree without sibling scans; dirty, unborn, detached, in-progress, gitlink, invalid-remote, and contaminated-environment inputs fail before provisioning; symlink aliases map to one source identity; linked-worktree sources produce self-contained session repositories; source changes do not appear in sessions and session changes do not appear in sources; session `.git` common paths and objects remain usable after the source is unavailable; source hooks, filters, config injection, credential helpers, and non-file transports do not execute during provisioning or apply; and hostile identifiers or persisted paths cannot escape the managed data root.

Store and orchestration checks prove owner-only modes, atomic and durable record replacement, bounded locking, one checkout for concurrent default starts, distinct checkouts for `--new`, exact-base capture under source movement, live and stale lease behavior, deterministic recovery from every transitional state, safe refusal of corrupt or symlinked state, no deletion outside a validated session root, and durable evidence for success, refusal, timeout, interruption, and reconciliation.

Apply checks prove deterministic result commits, no-op results, ancestry refusal, exact-base fast-forward, source identity and branch validation, clean-tree validation immediately before mutation, no source mutation on every precondition failure, no automatic merge for moved HEAD, retained session data after every failure, idempotent repeated apply, post-apply HEAD/index/tree verification, and reconciliation of interrupted apply facts. Container and CLI checks prove that only the private checkout is mounted at `/workspace`, initial entry and re-entry select the same session, structured agent arguments are preserved, ambiguous non-interactive agent selection fails with remediation, raw control bytes never reach terminal output, and compatibility commands cannot restore parent mounts or bypass governance.

## Consequences

The ordinary flow becomes `cd repo && ai-agent start`: one explicit repository, one selected identity, one private durable workspace, one governed runtime, and one explicit integration step. Local provisioning avoids remote latency and availability while preventing concurrent human edits from changing the agent's view. Agent edits cannot leak into the human checkout before apply, linked-worktree metadata does not cross the container boundary, sibling repositories are not exposed, and the source commit for every result is auditable.

The cost is additional local disk and checkout time, strict refusal of dirty, detached, and submodule cases, committed blob rather than host-filtered materialization during the first implementation, and a managed session lifecycle that must be recovered and pruned. The budgets and evidence make those costs observable, while self-contained checkouts and deterministic states favor recovery and maintainability over clever sharing. Expert shared-checkout and working-tree-snapshot modes may be designed later, but they must be explicit capabilities with separate names, tests, evidence, and security claims; they cannot weaken this default contract.
