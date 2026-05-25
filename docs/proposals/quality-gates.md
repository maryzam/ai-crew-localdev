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
| 1 | `make verify` as the single "done" gate                      | Phase 4 — quality loops               | S      | Wraps `go test -race`, integration build, vet, lint, doc-link check.  |
| 2 | Doc-drift detector (identifiers in docs vs. code)            | Phase 1 — productize / Phase 4        | S      | Catches every doc-staleness finding from Rounds 3 and 4.              |
| 3 | Inline-comments lint rule                                    | Phase 4 — quality loops               | S      | Forces decomposition over narrative comments.                         |
| 4 | Pre-commit ADR-or-opt-out hook on sensitive paths            | Phase 4 — quality loops               | S      | Forces contract-first habit on `internal/broker/`, `internal/policy/`. |
| 5 | Branch hygiene check (local vs. remote tracking)             | Phase 1 — productize                  | XS     | Prevents the `pr-45` vs. `refactor/credential-generic-broker` mishap. |
| 6 | Adversarial-review agent as a PR check                       | Phase 4 — auto-review triggers        | L      | Highest single-feature leverage. PR #45 is its own benchmark.          |
| 7 | Call-site sweep tool (diff-aware)                            | Phase 4 — auto-review triggers        | M      | Catches "I changed `Foo`'s semantics but missed N callers."           |
| 8 | Invariant-tests-as-coverage gate                             | Phase 4 — invariant tests             | M      | New exported method or wire field requires matching `_invariants_test.go`. |
| 9 | PR description template with enforced sections               | Phase 4 — PR risk tiers               | XS     | "Invariants preserved" / "Call sites swept" / "Build tags exercised". |
| 10 | Claim-vs-reality verifier (over-claim detection)            | Phase 5 — meta-agent optimization     | L      | Logs each "ready" claim, re-runs `make verify`, traces over-claim rate to Langfuse. |
| 11 | Property-based / invariant-as-spec test scaffolding          | Phase 4 — quality loops               | M      | Cross-agent cache isolation etc. are naturally properties, not examples. |
| 12 | Spec-as-failing-tests workflow                               | Phase 4 — quality loops (new sub-line) | M      | First commit of a `refactor:` PR is failing `_invariants_test.go`.    |
| 13 | Threat-model template per credential provider                | **New — Security expansion**          | M      | `docs/threats/<provider>.md` required for any new `CredentialProvider`. |
| 14 | Langfuse-backed over-claim signal                            | Phase 5 — meta-agent optimization     | M      | Counts rework rounds per PR; flags PRs whose ratio exceeds the team baseline. |
| 15 | Container hardening parity check                             | Phase 4 — quality loops               | S      | Parses guarantees from `user-manual.md`; asserts they match `devcontainer.json`. |

Effort key: XS ≤ 1h, S ≤ 1d, M ≤ 3d, L ≤ 1w.

## Tier 1 — Cheap, ship first

Targets ≥ 60% of Round-2/3/4 findings at ~1 day total work.

### 1. `make verify` as the single "done" line  (Phase 4)

Today `make test` is `go test ./...`. The agent (and humans) call something done when tests pass. That signal is too weak — Round 4 #1 was an integration-tagged test that didn't compile.

Replace `make test` with `make verify` that runs:

```make
verify: build
        go test -race -count=1 ./...
        go build -tags integration ./...
        go vet ./...
        go vet -tags integration ./...
        golangci-lint run
        scripts/check-doc-drift.sh
```

"Ready for review" claims that aren't preceded by a clean `make verify` should be treated as unverified.

### 2. Doc-drift detector  (Phase 1 + Phase 4)

A small script (`scripts/check-doc-drift.sh`) that:

1. Greps `docs/**.md` for backtick-quoted identifiers (`repo_not_allowed`, `MintToken`, `AllowedRepos`, `ai-agent-policy/v1`, ...)
2. Filters for likely Go-symbol shapes (snake_case constants, CamelCase types)
3. Fails if any aren't present in the codebase

Round-3 #4 (`allowed_repos` in user-manual) and Round-4 #4 (`repo_not_allowed` in user-manual) would both have been caught by this. ~50 LOC of Bash/Go.

### 3. Inline-comments lint rule  (Phase 4)

A golangci-lint custom analyzer or `go vet` pass that flags comments inside function bodies (not godoc on exported symbols). Hard-fails CI.

Forces the discipline the user has had to re-state across multiple rounds.

### 4. Pre-commit ADR-or-opt-out hook  (Phase 4)

`.githooks/pre-commit` already runs lint. Extend with:

- If the staged diff touches `internal/broker/`, `internal/policy/`, or modifies the `CredentialProvider` interface, require either a new file in `docs/decisions/` or a `[no-adr]` token in the commit message.
- Same for `_invariants_test.go` additions: PRs that add a new exported method without matching invariant assertions warn (or fail under a stricter mode).

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

### 7. Call-site sweep tool  (Phase 4)

Given a diff, emit a structured report:

```
Modified exports:
  policy.Validate  - semantic change: now schema-only
    callers:
      cmd/ai-agent-broker/main.go:58       — still calls; expectation OK (broker continues with computeAgentConfigs)
      internal/cli/policy_validate.go:42   — STILL CALLS; expectation BROKEN (CLI now ships a schema-only check) ⚠
      internal/cli/doctor.go:402            — STILL CALLS; expectation BROKEN ⚠
```

Required output in every PR description. Forces the call-site sweep that I skip on my own.

### 8. Invariant-tests-as-coverage gate  (Phase 4)

The repo has the `_invariants_test.go` convention; the discipline isn't enforced. Add a pre-commit/CI script that:

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

### 12. Spec-as-failing-tests workflow  (Phase 4 — new sub-line)

For any PR labeled `refactor:` or touching the broker core: the first commit must be `_invariants_test.go` files that fail. The implementation commits make them pass. Enforced by CI inspecting the commit sequence on the branch.

This inverts the bias I have toward writing implementation first and then writing tests that confirm what the implementation already does.

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

A unifying concept under Phase 4 that pulls together: `make verify` as the only "done" line, the adversarial pre-review agent, the call-site sweep tool, the claim-vs-reality verifier, and the spec-as-failing-tests workflow. The end state: an agent's "ready" claim is mechanically verifiable before a human looks; an over-claiming agent is detectable in Langfuse.

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

Strictly in order of leverage-per-effort:

1. **Item 1** (`make verify`) — XS, ~30 min
2. **Item 2** (doc-drift detector) — S, ~half day
3. **Item 3** (no-inline-comments linter) — S, golangci-lint config + custom analyzer or `go vet`
4. **Item 5** (branch hygiene hook) — XS
5. **Item 9** (PR description template) — XS
6. **Item 4** (ADR pre-commit hook) — S
7. **Item 6** (adversarial review agent) — L, but highest single-feature leverage and has a built-in benchmark (PR #45's findings)

Tier 1 items 1–5 are a 1–2 day effort that would close ~60% of the rework cost on a future PR like #45. Item 6 is the discrete bet worth making after Tier 1 lands.
