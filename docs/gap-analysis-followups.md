# Gap Analysis Follow-Ups

This is the working backlog of the remaining product gaps after the four
"usable today" P0 pull requests (#52–#55) merged. It is a companion to
`gap-analysis.md`: that file stays the source of truth for which gaps are open,
and this file records how the remaining ones are sequenced into pull requests.

Per the `gap-analysis.md` Completion Rule, a gap leaves the table only when its
end-user behavior is implemented, covered by the supported workflow, and
validated by an end-to-end test. The P0 implementations have merged, but none of
their gaps can be retired until the end-to-end validation below exists. That is
why the prioritized-gaps table is still untouched.

## Status after PRs #52–#55

| PR | Shipped | Validation still owed |
|---|---|---|
| #52 | Session credential integrity: bind secret no longer persisted, sessions revoked on agent exit, unmanaged `gh` removed from `PATH`. | Real brokered push proving personal-credential bypass is closed end to end. |
| #53 | Frictionless onboarding: `make install`, `ai-agent install` units, non-interactive `ai-agent setup`. | Journey test from a clean host through install and setup. |
| #54 | Persistent agent home via a named volume. | Restart and re-entry test proving home survives. |
| #55 | Project-aware provisioning, first slice: `ai-agent up --project` honors a project's own devcontainer and injects a read-only broker overlay. | Real-container test: broker reachable, brokered push, read-only overlay, shell entry on a minimal base. |

## Remaining pull requests

### PR 5 (next) — Real-container end-to-end validation, then retire proven gaps

Close the loop that PR #55 deferred and that the Completion Rule requires.

1. Add a real-container end-to-end test (build tag `integration`, in
   `internal/e2e/`, following the existing `devcontainer_cli_test.go` and
   `devcontainer_home_persistence_test.go` harness) that exercises
   `ai-agent up --project` against a repository carrying its own devcontainer:
   - the project's own devcontainer comes up, not the generic image;
   - the broker socket is reachable from inside the project container;
   - a brokered `git push` or `gh` call works through the injected toolchain on
     `PATH`;
   - the overlay mounts are read-only — writes to the runtime dir and the
     injected binaries are rejected — and shell entry works on a minimal
     (Alpine) base.
2. Extend coverage toward the P1 "complete user journey" gap: install and setup,
   then up, then restart and re-entry with the persistent home, then a real
   brokered push. Reuse PR #54's persistence harness rather than duplicating it.
3. Only after the tests pass against a real runtime, update `gap-analysis.md` to
   retire the P0 gaps that are now both implemented and validated. Leave anything
   still unproven (project-declared secrets, caches, and services; the portable
   toolchain Feature) in the table.

Run with `make readiness-devcontainer`, extending it or adding a sibling target
as needed. Podman and the devcontainer CLI are the local runtime; prefer Podman.

### PR 6 — Portable toolchain injection via a devcontainer Feature

Replace PR #55's host-binary bind-mount overlay with a devcontainer Feature that
installs the `ai-agent` toolchain inside the project container, removing the
single-user, single-architecture assumption of bind-mounting host binaries.

This depends on the binary and image distribution that PR #3 deferred: first
decide how the toolchain is published and fetched (release artifacts or an
image), which likely needs a decision record under `docs/decisions/`. Keep the
read-only broker socket mount and the socket and `PATH` environment wiring; only
the binary-delivery mechanism changes.

### PR 7 — Project-declared secrets, caches, and service wiring

Extend `ai-agent up --project` beyond what a project's own devcontainer already
expresses: project-declared secrets, build and dependency caches, and service
wiring. Design the declaration format first — an ai-agent overlay manifest rather
than edits to the project's devcontainer config — and how secrets reach the
container through the broker instead of being baked into an image. This is the
larger, least-defined slice; propose a design and a thin first slice before
implementing.

## Conventions for each pull request

- Self-documenting code: no comments inside function bodies (the inline-comment
  semantic gate rejects them; put rationale in doc comments). The pre-push hook
  mirrors this gate locally.
- Broker and policy changes need same-package tests (the invariant gate); the
  decision-record gate applies to architectural changes.
- `make verify` is the full local gate; `make build`, `make test`, and
  `make lint` are the focused ones.
- Branch from `main`, keep each pull request to a single change surface, and do
  not touch unrelated files.
