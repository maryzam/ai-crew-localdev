# Design Principles

**Scope (builder track): the principles a contributor should optimize for — keep the wrapper lean, keep the UX invisible, keep quality a product contract, and make the tool pleasant to live in.** These sit above the enforceable rules in [AGENTS.md](../../AGENTS.md) and the architecture in [Architecture](architecture.md).

## Keep the wrapper lean

The tool is a governance layer around agents that are already heavy. It must not become the reason a laptop struggles.

- **One deployable.** The CLI, broker, and shims are one multi-call binary. No daemons a user has to reason about beyond the broker, and the broker is socket-activated so it only runs when a session connects.
- **The container is the cost, not the wrapper.** The generic image carries the toolchains; the `ai-agent` overhead on top is a static binary and a Unix socket. Measure any new runtime observer, relay, or budget hook against a budget — added memory and startup time are regressions unless justified by emitted evidence.
- **Bounded everything.** Logs, retained evidence, telemetry payloads, findings, retries, and export queues all have explicit caps with deterministic overflow behavior. A feature that can grow without a bound does not ship.
- **Prefer native telemetry over instrumentation.** Usage comes from the agents' own OpenTelemetry output through a loopback relay, not from wrapping or parsing agent internals. Less code in the hot path, less to break on an agent upgrade.

## Keep the UX invisible

The fastest way to lose a user is to make the governed path slower or fussier than running `claude` bare. The brokered path should feel like the native CLI, only safer.

- **`git push` and `gh pr create` must just work** inside a session, with no extra flags — auth happens under them.
- **One command to start** (`ai-agent up`), with guided setup when config is missing, and a printed re-entry command so the second session is trivial.
- **Fail with the fix, not just the symptom.** `ai-agent doctor` names the broken check and its remediation; every error should point at the next action.
- **Never make security a manual chore.** If a best practice matters, enforce it (PEM permissions, `gh auth` block) or surface it (rotation reminder) — do not leave it as documentation the user must remember. When you find advice in the docs that the tool could enforce or check, that is a bug to file, not a paragraph to polish.

## Quality is a product contract

Verification is not a best-effort script. Project manifests declare quality gates that managed runs enforce, retry, and record. Efficiency work is held to the same bar: treat efficiency changes as product changes — compare similar tasks against the same quality gate, and reject changes that reduce quality or security evidence.

## Make it pleasant to live in

A governance tool people actually keep using is one that feels good, not just correct. Small, honest touches are in scope as long as they never obscure a security signal:

- Clear, human status output — who is signed in, what a run cost, what it touched.
- Progress and outcome cues on the long operations (`up`, build, a managed run) so the tool feels responsive.
- Room for light gamification of good habits — streaks of clean verified runs, a nudge when a resource has gone unused, a satisfying "all checks passed" — provided it is advisory, bounded, and never competes with a real warning for attention.

Anything in this section is a nicety; it yields immediately to the enforced invariants in [Security Design](security-design.md) and the rules in [AGENTS.md](../../AGENTS.md).

## Known boundary debt

The single-owner boundaries for secure-file validation, asset staging, trusted source, readiness, and security-claim publication are in place. Readiness is model-driven: checks are declared in a spec registry (`internal/app/readiness/specs.go`) with owner and severity, rendering is pure presentation over the model, `--json` carries machine-readable evidence, and the check table in the CLI reference is generated from the registry (`make readiness-docs`) with a drift test. Security invariants are generated from `internal/quality/securityclaims`, so the table cannot drift away from named executable proofs. Three items remain tracked deliberately, not overlooked:

- **Materialization lifecycle.** `embedasset.Materialize` owns atomic copy and parity but not stale-file cleanup, directory fsync, recursive trees, or emitted evidence (source kind, asset-set hash, destination, modes). Naive cleanup is unsafe here: the Langfuse stage directory also holds the user's generated `.env`, so a "delete files not in the source set" pass would destroy it. Cleanup must be a per-group policy with evidence, not a blanket sweep.
- **Runtime identity boundary.** The devcontainer marker rejects accidental host-native runs, but it is not a kernel identity or unforgeable capability. The north-star proof is broker authorization by distinct container peer UID with idmapped mounts, or a pathless connected-fd capability with explicit reconnect semantics.
- **Runtime identity evidence.** Identity now canonicalizes the workspace (symlinks resolved) and is workspace-scoped, but does not yet emit rebuild/reuse evidence or define an explicit collision and moved-workspace policy.

Container-level end-to-end proof (release binary with no checkout, brokered push, two workspaces producing distinct containers) lives in `make journey`; the non-container claims are pinned by the boundary and generated claim tests, with env-var drift already covered by `scripts/check-env-contract.sh`.
