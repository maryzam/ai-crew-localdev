# AI-Crew Development Workflow Architecture

## Problem Statement

Solo developer orchestrating three AI agents (Claude Code, Codex, Gemini/Jules) across a security-critical Go project. Current workflow produces a 27% rework rate — not from linting or test failures, but from conceptual gaps: architectural misalignment, scope drift, naive security implementations, and agents modifying tests to pass rather than fixing code.

The human is the bottleneck at every gate. PR review trails are the only observability layer. Moving reviews earlier would improve speed but destroy visibility into agent failure modes.

## Root Causes

| Cause | Evidence |
|---|---|
| Specs exist as prose, not executable contracts | "fail closed when broker unreachable" was in the arch doc; the implementation that violated it compiled and passed tests |
| No invariant tests — agents write tests that validate their own output | `_test.go` files test what was built, not what should have been built |
| Review findings are conceptual (security, architecture), not syntactic | 1 of 32 review findings would have been caught by a linter |
| Observability is coupled to the review gate | Can't optimize review flow without losing agent performance data |
| Agents don't generalize fixes | Same credential-bypass class of bug appeared in PR #13 and PR #14 |
| Docs drift from implementation | 12 docs commits vs 13 feat commits; multiple "align docs with implementation" fixups |

## Architecture Decisions

### 1. Observability Backend: Langfuse (self-hosted)

**Choice:** Langfuse v3 via single `docker-compose.yml` (6 containers: web, worker, Postgres, ClickHouse, Redis, MinIO).

**Why Langfuse over Phoenix:**
- `session_id` groups all agent activity for one issue into a single view — critical for cross-agent workflows
- 10 typed observations (agent, tool, event, generation) map directly to dev workflow actions
- Programmatic scoring API — Codex reviewer can POST quality scores to the trace it reviewed
- REST API fallback for agents with weak native logging (Gemini/Jules)
- Filterable custom metadata via `langfuse.*` attribute prefix
- All features MIT-licensed, no feature gates

**Why not lighter alternatives:**
- Phoenix (single container, ~500MB) lacks session grouping, typed observations, and scoring — the three features that make multi-agent analytics possible
- Git notes alone give permanence but no dashboard, no filtering, no trend analysis

**Resource profile:** ~2-3GB RAM idle on a dev workstation. Acceptable trade-off for full multi-agent observability.

### 2. Quality Gate: Invariant Tests, Not Review Rounds

**Choice:** Encode architectural and security invariants as `_invariants_test.go` files that exist before implementation begins. Agents implement against failing tests, not prose specs.

**Why:**
- Review findings are 87% conceptual (security, architecture, scope) — linting and unit tests don't catch them
- Prose specs don't fail builds; test assertions do
- Agents that write their own tests optimise for passing, not correctness
- Invariant tests are the shared deterministic context that actually works across all agents

**Convention:**
- `*_invariants_test.go` — security and architecture contracts, written during scoping, before implementation
- These tests are the source of truth for "what must be true," replacing prose spec validation
- Regular `*_test.go` — implementation tests, written by the implementing agent

### 3. PR Tiers with Auto-Merge

**Choice:** Three tiers based on blast radius, not all PRs gated equally.

| Tier | Criteria | Gate |
|---|---|---|
| T1: Auto-merge | Docs-only, test-only, no `.go` changes outside `*_test.go` | CI passes + Codex review finds 0 issues |
| T2: Light review | Single-package changes, no broker/launcher/shims touched | CI + Codex review + human skims diff |
| T3: Full review | Touches broker, launcher, shims, devcontainer, or auth paths | CI + Codex review + human explicit approval |

**Why:** The human reviewing every PR is the single largest time sink. T1 and T2 changes don't warrant it. Reserve human attention for security-critical paths.

### 4. Automated Review Trigger, Not Manual

**Choice:** Codex review triggers automatically on PR open via GitHub Actions, not manually invoked.

**Why:** Codex-as-reviewer is the highest-value quality gate in the current flow (found 14 security issues, 0 false positives). But it runs only when manually triggered, creating async wait. Automate the trigger; keep the review.

### 5. Observability Decoupled from Review Gate

**Choice:** All agent actions logged to Langfuse regardless of whether a review happens. Git notes as permanent audit trail.

**Integration points:**
```
Claude Code hooks ──→ OTel SDK ──→ Langfuse OTLP endpoint (localhost:3000/api/public/otel)
Codex OTel export ──→ OTLP ─────→ Langfuse OTLP endpoint
Gemini/Jules ──────→ git hook ──→ Langfuse REST API (curl POST)
All agents ────────→ git notes ─→ refs/notes/agent-log (permanent record)
```

**Why decouple:** Moving review earlier (pre-push) would lose the PR trail that currently serves as the only agent activity log. With Langfuse capturing everything independently, the review can move to wherever it's most efficient without sacrificing visibility.

**Trace identity convention (multi-repo safe):**

Every trace, score, and log entry must be unambiguously scoped to a repository. Without explicit repo context, issue numbers collide across repos and Langfuse dashboards become unreliable.

| Field | Convention | Example |
|---|---|---|
| `metadata.repo` | `{github_org}/{repo_name}` on all Langfuse traces | `ai-crew/localdev` |
| `session_id` | `{repo_short_name}#{issue_number}` | `localdev#18` |
| OTel `service.name` | repo short name (standard OTel resource attribute, Langfuse respects it) | `localdev` |
| Git notes ref | `refs/notes/agent-log/{repo_short_name}` | `refs/notes/agent-log/localdev` |

- `metadata.repo` is the primary key for filtering — dashboard views, scoring queries, and trend analysis all filter on this field first
- `session_id` includes the repo prefix so cross-repo issue numbers never collide in session views
- `service.name` aligns with OTel conventions, giving Langfuse automatic service-level grouping without custom config
- Git notes use a repo-namespaced ref so aggregated notes (if ever pulled into a shared repo) stay partitioned

### 6. Smaller, Isolated Changes

**Choice:** One concern per PR, not one phase per PR.

**Evidence:** Phase PRs (#11, #13) averaged 16+ review rounds. Issue-scoped PRs (#21-24) averaged 1.5 rounds. Smaller scope = faster convergence = less rework.

### 7. Post-Merge Smoke Tests

**Choice:** `ai-agent doctor --mode host --json` and container smoke test run automatically after merge to main. Failure triggers auto-revert + logged event.

**Why:** Catches integration breakage that unit tests miss. The revert log feeds back into risk criteria — if a category of change keeps reverting, it graduates to a higher PR tier.

## Target Workflow

```
Human: scope issue + write invariant tests (the contract)
  │
  ▼
Claude Code: implement against failing invariant tests
  │ ← logged to Langfuse (session_id = {repo}#{issue})
  ▼
CI: build + test + lint + invariant tests
  │ ← pass/fail logged
  ▼
PR opens → Codex review triggers automatically
  │ ← findings logged with categories + scores
  ▼
  ├─ T1 (docs/tests): auto-merge if CI + review clean
  ├─ T2 (single-package): human skims diff
  └─ T3 (security-critical): human reviews thoroughly
  │
  ▼
Post-merge: doctor smoke test
  │ ← result logged
  ├─ Pass: done
  └─ Fail: auto-revert → log revert reason → escalate tier criteria
```

**Human touch points:** scope + invariant tests, T2/T3 review, revert triage.
**Everything else is automated and logged.**

## North Star Metrics

### Primary: First-Pass Acceptance Rate

**Definition:** Percentage of PRs that pass Codex review with zero `CHANGES_REQUESTED` findings on the first submission.

**Current baseline:** ~30% (estimated from commit history — most PRs have 1-3 fix rounds).
**Target:** >70% within 30 days of invariant test adoption.

**Why this metric:** It directly measures the quality of initial implementation. High first-pass rate means specs are clear, agents understand the contract, and the right things are tested before code is written. It captures all root causes simultaneously: unclear specs show up as scope findings, missing invariants show up as security findings, naive implementations fail the invariant tests before PR.

### Supporting Metrics

| Metric | What It Measures | Source |
|---|---|---|
| **Fix-to-feat commit ratio** | Rework volume | `git log` conventional commits |
| **Review findings by category** (security / architecture / scope / naive) | Where agents are weak | Langfuse scored traces |
| **Mean time from PR open to merge** | Flow efficiency | GitHub API |
| **Auto-merge rate** (T1 PRs merged without human) | Automation coverage | GitHub Actions logs |
| **Revert frequency** | Post-merge quality | git revert events in Langfuse |
| **Invariant test coverage** | Spec-to-test encoding completeness | `grep -r _invariants_test.go` |

### Maturity Levels

| Level | First-Pass Rate | Fix:Feat Ratio | Auto-Merge Rate | Description |
|---|---|---|---|---|
| **L0 — Current** | ~30% | 1.9:1 | 0% | Human gates everything, no observability |
| **L1 — Instrumented** | 30-40% | 1.5:1 | 0% | Langfuse logging active, metrics visible, no flow changes yet |
| **L2 — Contracted** | 50-60% | 1:1 | 10-20% | Invariant tests adopted, T1 auto-merge active |
| **L3 — Automated** | 70%+ | 0.5:1 | 30-50% | Full tier system, automated review triggers, post-merge smoke |
| **L4 — Self-Correcting** | 80%+ | 0.3:1 | 50%+ | Revert-driven tier escalation, agent-specific coaching prompts derived from Langfuse patterns |
