# Proposal: Quality Gates for the Agent–Human Trust Contract

**Status:** Proposed
**Date:** 2026-05-25
**Origin:** Retrospective on PR #45 (credential-generic broker refactor)

## Why this exists

PR #45 produced **19 distinct review findings across 4 review rounds**. Every finding was real (none rejected as "actually fine"). The mix included 1 critical security regression (privilege escalation), 4 high-severity gaps (cache cross-leak, racy reload, broken e2e, CLI validation gap), 9 medium and 5 low.

The pattern across rounds was consistent: the agent claimed "ready, verified end to end" and the human reviewer then found real bugs that pre-PR self-verification had missed. Each finding maps cleanly to a feature the tool should make hard to skip — i.e. the retrospective is a feature spec.

This proposal pulls those features out, ties them to the README roadmap, and flags items that need new roadmap surface.

## Roadmap alignment summary

| # | Improvement                                                  | README phase                          | Effort | Notes                                                                 |
|---|--------------------------------------------------------------|---------------------------------------|--------|-----------------------------------------------------------------------|
| 1 | `make verify` as the single "done" gate                      | Phase 4 — quality loops               | S      | Wraps `go test -race`, integration *test compile* (`go test -run '^$'`), vet, lint, docs gate. |
| 2 | Doc-drift detection (commodity tools + executable examples)  | Phase 1 — productize / Phase 4        | S      | Link/style/spell via commodity tools; schema-key drift via new executable-examples test (item 2 details). |
| 3 | Inline-comments lint rule                                    | Phase 4 — quality loops               | S      | Forces decomposition over narrative comments.                         |
| 4 | ADR-or-opt-out gate (`commit-msg` hook + CI check)           | Phase 4 — quality loops               | S      | Forces contract-first habit on `internal/broker/`, `internal/policy/`. |
| 5 | Branch hygiene check (local vs. remote tracking)             | Phase 1 — productize                  | XS     | Prevents the `pr-45` vs. `refactor/credential-generic-broker` mishap. |
| 6 | Adversarial-review agent as a PR check                       | Phase 4 — auto-review triggers        | L      | Highest single-feature leverage. PR #45 is its own benchmark.          |
| 7 | Call-site sweep report (discovery-only)                      | Phase 4 — auto-review triggers        | M      | Surfaces callers of modified exported symbols; semantic-change judgment is left to the review agent (#6) or human reviewer. |
| 8 | Invariant-tests-as-coverage gate                             | Phase 4 — invariant tests             | M      | New exported method or wire field requires matching `_invariants_test.go`. |
| 9 | PR description template with enforced sections               | Phase 4 — PR risk tiers               | XS     | "Invariants preserved" / "Call sites swept" / "Build tags exercised". |
| 10 | Claim-vs-reality verifier (over-claim detection)            | Phase 5 — meta-agent optimization     | L      | Logs each "ready" claim, re-runs `make verify`, traces over-claim rate to Langfuse. |
| 11 | Property-based / invariant-as-spec test scaffolding          | Phase 4 — quality loops               | M      | Cross-agent cache isolation etc. are naturally properties, not examples. |
| 12 | Spec-as-tests discipline for high-risk paths                 | Phase 4 — quality loops (new sub-line) | M      | Final-diff gate: PRs touching broker/policy/providers must include matching invariant-test changes. History-shape neutral. |
| 13 | Threat-model template per credential provider                | **New — Security expansion**          | M      | `docs/threats/<provider>.md` required for any new `CredentialProvider`. |
| 14 | Langfuse-backed over-claim signal                            | Phase 5 — meta-agent optimization     | M      | Counts rework rounds per PR; flags PRs whose ratio exceeds the team baseline. |
| 15 | Container hardening parity check                             | Phase 4 — quality loops               | S      | Parses guarantees from `user-manual.md`; asserts they match `devcontainer.json`. |

Effort key: XS ≤ 1h, S ≤ 1d, M ≤ 3d, L ≤ 1w.

## Tier 1 — Cheap, ship first

Items 1–5 below plus item 9 (Tier 2) form the cheap front of the proposal. Total realistic effort is roughly two engineering days; they target ≥ 60% of the Round-2/3/4 rework cost.

### 1. `make verify` as the single "done" line  (Phase 4)

Today `make test` is `go test ./...`. The agent (and humans) call something done when tests pass. That signal is too weak — Round 4 #1 was an integration-tagged test that did not compile.

Replace `make test` with `make verify` that runs:

```make
verify: build docs-check
        go test -race -count=1 ./...
        go test -tags integration -run '^$$' ./...
        go vet ./...
        go vet -tags integration ./...
        golangci-lint run

docs-check:
        lychee --no-progress 'docs/**/*.md' README.md
        markdownlint-cli2 'docs/**/*.md' 'README.md'
        codespell docs/ README.md
```

`docs-check` runs commodity tooling only: link health, markdown style, English typos. It does *not* catch schema-key drift in documented JSON examples — see item 2 for the separate executable-examples deliverable that does.

The Makefile recipe escapes `$` as `$$`; at the shell prompt it's `go test -tags integration -run '^$' ./...`. That is the standard "compile but do not execute" gate for tagged test files. `go build -tags integration ./...` is *not* sufficient — `go build` skips `_test.go` files entirely, so the failure mode Round 4 #1 reported would still slip through. Empirically verified: a syntactically broken `_test.go` under a build tag passes `go build ./...` with exit 0; only `go test -run '^$'` (or `go vet`) surfaces it.

"Ready for review" claims that aren't preceded by a clean `make verify` should be treated as unverified.

### 2. Doc-drift detection  (Phase 1 + Phase 4)

Use commodity tooling for the standard cases. A naive grep-the-backticks script is fragile in both directions: it misses *unquoted* stale references (most prose mistakes), and it false-positives on shell commands, file paths, schema/config keys, and intentional non-Go examples.

Three layers, in order of cost:

- **Commodity tooling (install-and-go).** Wired into `docs-check`:
  - *Link health:* [`lychee`](https://github.com/lycheeverse/lychee) or `markdown-link-check` over every link in `docs/`. Catches broken cross-doc references and dead URLs.
  - *Style + structure:* `markdownlint-cli2` for heading/list/code-fence consistency.
  - *Spelling / typos:* `cspell` or `codespell`. Catches ordinary English typos in prose. They do *not* catch domain-key mismatches like `pull_request` vs `pull_requests` — both words are spelled correctly.
- **Executable examples (new work, ~half day).** A new `internal/docsexamples` test (or equivalent) that:
  1. Scans `docs/**/*.md` for fenced JSON code blocks tagged as policy / identities / provider config examples.
  2. Feeds each block through `broker.ValidatePolicy` (or the matching parser) with a `validatorProviders(...)` setup mirroring `setup.go`.
  3. Fails if any example does not round-trip cleanly. This is the layer that catches schema-key mismatches like `pull_request` vs `pull_requests`, because the GitHub provider's `ParseConfig` rejects unknown permission keys (introduced in PR #45 round-4 #3). `policy.Validate` alone is schema-only and does *not* run that check — the provider-aware path is required.
- **Narrow identifier allowlist (small script, last-resort).** A few dozen lines that grep for documented broker error codes (`resource_not_allowed`, `unknown_credential_type`, ...) and wire methods (`mint_credential`, ...) and fail if any aren't present in the codebase. The allowlist is small (low double digits) and lives alongside the constants it mirrors, so adding a new error code adds a single line.

Round-3 #4 (`allowed_repos` in docs) and Round-4 #4 (`repo_not_allowed` in docs) would have been caught: the first by the executable-examples test (the `AllowedRepos` field no longer exists on `policy.AgentPolicy`, so the example fails to parse); the second by the narrow identifier allowlist (the error code is gone from the codebase). Neither would have been caught by the commodity-tooling layer alone — that layer covers links, style, and English typos, not schema or identifier semantics.

### 3. Inline-comments lint rule  (Phase 4)

A golangci-lint custom analyzer or `go vet` pass that flags comments inside function bodies (not godoc on exported symbols). Hard-fails CI.

Forces the discipline the user has had to re-state across multiple rounds.

### 4. ADR-or-opt-out gate  (Phase 4)

Two layers, because each catches the failure mode the other misses:

- **Local (`commit-msg` hook, not `pre-commit`).** The commit message is composed *after* `pre-commit` runs, so the `[no-adr]` opt-out token can only be read from a `commit-msg` hook. The hook inspects the staged diff: if it touches `internal/broker/`, `internal/policy/`, or modifies the `CredentialProvider` interface, the commit message must either contain `[no-adr]` or the diff must add a new file under `docs/decisions/`. Local hooks are opt-in (require `make setup-hooks` to activate), so this layer is fast-feedback for developers who opt in.
- **CI / PR check (required, not opt-in).** A GitHub Action does the same diff inspection against the PR's *combined* diff (not per-commit), reads the PR description for the opt-out token, and fails the check otherwise. This is the layer that actually blocks merge.

Same dual-layer pattern applies to `_invariants_test.go` coverage: warn locally on opt-in hooks, fail in CI on the combined diff.

### 5. Branch hygiene check  (Phase 1)

A pre-push hook that fails if `git config branch.<current>.merge` doesn't match the branch being pushed to. Prevents the local-branch-name vs. PR-branch-name drift that happened mid-PR.

## Tier 2 — Bigger leverage, real design

### 6. Adversarial-review agent as a PR check  (Phase 4)

**Highest single-feature leverage.** Phase 4 of the roadmap already calls for "auto-review triggers"; this is the concrete deliverable.

A Claude (or any LLM) agent that runs on every push to a non-main branch with a fixed prompt:

```
Read this diff hostilely. For every modified public function:
  - list all callers and whether their expectations changed
For every new validation gate:
  - list other places that should also gate
For every security-sensitive path:
  - propose one attack you would try
For every removed/renamed identifier:
  - grep for stale references in docs and tests
Report findings as PR review comments.
```

PR #45 has a **ground-truth set of 19 findings** with severity labels. That's a benchmark. Iterate the prompt + tool wiring until the agent catches ≥80% of them automatically. Once it does, the human's review time is for architecture-level "is this the right design", not for bug-hunting that the model can do.

### 7. Call-site sweep report  (Phase 4)

Caller/reference discovery is commodity Go territory — no bespoke analyzer needed. The substrate exists in:

- `gopls` (`textDocument/references`, `callHierarchy/incomingCalls`)
- `go/packages` + `golang.org/x/tools/go/analysis` for custom analyzers
- `staticcheck` for pattern-based checks on the call graph

The only piece worth building is the **report layer** that takes a PR diff, identifies modified exported symbols, queries one of the above for all references, and renders a structured summary:

```
Modified exports:
  policy.Validate
    callers (5):
      cmd/ai-agent-broker/main.go:58
      internal/cli/policy_validate.go:42
      internal/cli/doctor.go:402
      internal/cli/setup.go:247
      internal/cli/setup.go:311
```

The "expectation BROKEN" judgment is not reliably automatable for a generic sweep — semantic change vs. signature-compatible change requires reading the diff in context. That part is an LLM prompt (the adversarial-review agent in item #6), not a static analyzer. The static layer surfaces the call sites; the LLM (or human reviewer) decides whether expectations changed.

Required output in every PR description. Forces the discovery step that a developer is likely to skip; leaves the judgment step to the reviewer or the review agent.

### 8. Invariant-tests-as-coverage gate  (Phase 4)

The repo has the `_invariants_test.go` convention; the discipline isn't enforced. Add a CI check (with a matching opt-in local hook for fast feedback, parallel to item 4) that:

- Compares the set of exported symbols in `internal/broker/api.go`, `provider.go`, etc. against asserted symbols in `*_invariants_test.go`
- Fails when a new exported symbol or wire field lands without at least one matching invariant assertion

### 9. PR description template  (Phase 4)

`.github/pull_request_template.md` with required sections:

- **Invariants preserved** (list test names that prove each)
- **Call sites swept** (output of tool #7 or manual list)
- **Build tags exercised** (`-race`, `-tags integration`, etc.)
- **Threat model delta** (only required if `internal/broker/providers/` or `internal/policy/` changed)

Sections left empty get a warning bot comment; the human can override but the friction is intentional.

### 10. Claim-vs-reality verifier  (Phase 5)

When a commit message or PR comment contains "ready for review", "verified end to end", or similar claim phrases, a downstream automation:

1. Re-runs `make verify` against HEAD
2. Reports the result back to the PR
3. Emits a Langfuse trace tagged with the agent / session / commit
4. Increments a per-PR "claim cycles" counter

Over time, the Langfuse data reveals which agents and which contexts over-claim. That's the exact signal `docs/dev-workflow-architecture.md` already says it wants.

## Tier 3 — Architectural

### 11. Property-based / invariant-as-spec test scaffolding  (Phase 4)

Cross-agent cache isolation, atomic reload, subset enforcement, and credential type ↔ resource provider match are all *properties*, not examples. A small wrapper around `testing/quick` or `pgregory.net/rapid` would express:

```go
rapid.Check(t, func(t *rapid.T) {
    a := genAgent(t); b := genAgent(t)
    rapid.PreCondition(t, configsDiffer(a, b))
    rapid.PreCondition(t, sameResource(a, b))
    require.NotEqual(t, mint(a), mint(b))
})
```

Adopt for security-critical invariants first.

### 12. Spec-as-tests discipline for high-risk paths  (Phase 4 — new sub-line)

Enforcing "first commit must be failing tests" via commit-sequence inspection fights rebases, squashes, amends, and the common branch-protection rule that every commit must be green. It's the wrong gate.

The commodity-friendly version is **diff-shape based, not history-shape based**:

- For PRs whose diff touches high-risk paths (`internal/broker/`, `internal/policy/`, `internal/broker/providers/`), CI requires that the *final* diff include changes to matching `_invariants_test.go` (or `*_test.go` under the same package) — not a specific commit ordering.
- `make verify` must be clean on the final HEAD.
- The PR checklist (item #9) carries a "Invariants preserved (list test names that prove each)" section that the reviewer signs off on.
- Pairing reviewer expectation + diff-shape gate + the adversarial review agent (item #6) gives the discipline without fighting git workflow norms.

This still inverts the implementation-first bias the retrospective surfaced, but by holding the final state to a contract instead of policing how commits got there.

### 13. Threat-model template per credential provider  (**New — Security expansion**)

This is a roadmap *gap*. Phases 4 and 5 cover quality and meta-monitoring but nothing explicitly covers the security review of a new credential surface.

When a new provider lands (`internal/broker/providers/<name>`), require `docs/threats/<name>.md` covering:

- Ambient escalation paths (what if the agent process is malicious?)
- Replay attacks against the wire protocol
- Behavior during policy reload
- Behavior on misconfiguration
- What data must never leave the broker

CI fails on PRs that add a `CredentialProvider` implementation without a matching threats doc.

### 14. Langfuse-backed over-claim signal  (Phase 5)

Layered on top of #10. After N rework rounds on a single PR, raise a meta-flag: *the spec is probably wrong, not just the code.* That's the cue to retreat to an architecture review session before continuing to patch.

### 15. Container hardening parity check  (Phase 4)

A test that:

1. Parses the security-guarantees table in `docs/user-manual.md` (`--cap-drop=ALL`, `--read-only`, etc.)
2. Asserts each guarantee is actually present in `.devcontainer/devcontainer.json`

Drift detection at the security-claim layer. Same idea as the doc-drift detector but for the *contract* between documentation and configuration.

## New themes (require roadmap surface)

Most items above slot under existing phases. Two themes are genuinely new and worth calling out in the README roadmap:

### A. The agent trust contract

A unifying concept under Phase 4 that pulls together: `make verify` as the only "done" line, the adversarial pre-review agent, the call-site sweep report, the claim-vs-reality verifier, and the spec-as-tests discipline (final-diff gate, not commit-history policing). The end state: an agent's "ready" claim is mechanically verifiable before a human looks; an over-claiming agent is detectable in Langfuse.

Suggested README phrasing for Phase 4: append "and a verifiable agent trust contract" to the focus line, with the proposal doc as the deliverable reference.

### B. Per-provider security review surface

A new minor strand — call it Phase 4b or a sub-line under Phase 4 — that requires explicit threat-model documents and parity checks whenever a new credential surface lands. Today Phase 4 implicitly assumes "security is non-negotiable" (the Core Value Proposition pillar) but has no concrete deliverable that grows with provider additions.

## Suggested README update

Phase 4's current line:

> Expand invariant tests, verify/retry flows, PR risk tiers, auto-review triggers, post-merge smoke checks, and revert-driven policy escalation.

Proposed expansion:

> Expand invariant tests, verify/retry flows, PR risk tiers, auto-review triggers (incl. adversarial pre-review), post-merge smoke checks, revert-driven policy escalation, and a verifiable agent-trust contract — every "ready" claim mechanically re-verified and threat-modeled before human review. See [docs/proposals/quality-gates.md](docs/proposals/quality-gates.md).

No new phase needed. The theme is already inside Phase 4's intent; the proposal sharpens it.

## What to ship first

Strictly in order of leverage-per-effort. Effort estimates are in the roadmap-alignment table; not duplicated here.

1. **Item 1** — `make verify` as the single done line.
2. **Item 5** — branch hygiene pre-push hook.
3. **Item 9** — PR description template with required sections.
4. **Item 3** — no-inline-comments lint rule.
5. **Item 2** — doc-drift detection (commodity tools first; executable-examples test second).
6. **Item 4** — ADR-or-opt-out gate (`commit-msg` hook + CI check).
7. **Item 6** — adversarial review agent. Highest single-feature leverage and has PR #45's 19 findings as a built-in benchmark.

The first six together fit roughly two engineering days. They close the bulk of the rework cost on a future PR like #45 without depending on the LLM-driven items. Item 6 is the discrete larger bet worth making once the cheaper gates are in place.
