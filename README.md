# AI Crew localdev

Secure, local-first AI development control plane for solo builders running multiple AI coding agents across multiple projects.

AI Crew localdev gives Claude Code, Codex, Gemini CLI, and future agents a hardened local workspace, brokered GitHub credentials, repo-scoped sessions, fail-closed `git`/`gh` access, and flow observability. The goal is simple: make AI-assisted development usable by solo founders of any technical level without handing every agent ambient access to the host, personal credentials, or every repo.

The north star is an autonomous, efficient, adaptive local dev environment: agents work inside governed project flows, quality is enforced through executable contracts, and a meta-agent layer monitors cross-project efficiency, resource use, token spend, and recurring failure patterns.

## What this repo contains

- Container config for the devcontainer flow
- Broker, launcher, helper, and wrapper binaries
- Supporting scripts for readiness and local validation
- Docs and fixtures for the brokered session model

## Core Value Proposition

| Pillar | What it means |
|--------|---------------|
| Simple to use | One supported bootstrap path with `ai-agent up`, preconfigured agent tooling, readiness checks, and a predictable workspace layout. |
| Local-first | Repos stay mounted from the local workstation; agents run in a local devcontainer with Podman-first runtime support and Docker fallback. |
| Secure by design | GitHub App keys stay in the host broker, credentials are minted on demand, sessions are repo-scoped, ambient credentials are scrubbed, and `git`/`gh` fail closed when the broker is unavailable. |
| Observable by flow | Langfuse and audit logs provide session, repo, agent, token, review, and verification traces so workflow quality can be measured instead of inferred from PR history. |
| Multi-project aware | Workspace layout, session identity, policy, and observability conventions are designed for solo builders juggling several active products or client projects. |

## Roadmap

Short, phased roadmap for turning the secure local foundation into a holistic solo-builder operating environment:

| Phase | Focus | Outcome |
|-------|-------|---------|
| 1. Productize the foundation | Smooth setup, clearer docs, guided GitHub App/policy creation, better `doctor` remediation, packaged install path. | A non-expert solo founder can get from install to first brokered agent session with minimal manual setup. |
| 2. Bring ECC-style intelligence | Add project-type skill packs, reusable rules, memory extraction, context budgeting, model/tool selection guidance, and prompt quality feedback. | Agents become more consistent across stacks and waste fewer tokens repeating known project context. |
| 3. Add operator cockpit | Build a local UI for active runs, agent status, diffs, test results, PR state, approvals, Langfuse traces, and token/resource use. | The user manages multi-agent work from one flow dashboard instead of scattered terminals, PRs, and logs. |
| 4. Automate quality loops | Expand invariant tests, verify/retry flows, PR risk tiers, auto-review triggers (incl. adversarial pre-review), post-merge smoke checks, revert-driven policy escalation, and a verifiable agent-trust contract — every "ready" claim mechanically re-verified and threat-modeled before human review. See [docs/proposals/quality-gates.md](docs/proposals/quality-gates.md). | Routine work advances with less human gating while high-risk changes still receive explicit review. |
| 5. Meta-agent optimization | Add cross-project monitoring for agent efficiency, recurring failure classes, token spend, idle loops, model choice, and project-specific coaching. | The system adapts to each project and agent, improving throughput and cost efficiency over time. |
| 6. Broaden beyond dev loop | Extend governed flows to launch, support, analytics, docs, customer research, release notes, and lightweight ops. | AI Crew becomes a local operating layer for the solo business, not only a coding sandbox. |

## Readiness Check

Run the devcontainer/container end-to-end readiness check with:

```bash
make readiness
```

That target runs `scripts/devcontainer-readiness.sh`, which launches the integration-tagged Go test
[`internal/e2e/devcontainer_readiness_test.go`](./internal/e2e/devcontainer_readiness_test.go).

The check:

- Starts a real broker on a Unix socket
- Launches the devcontainer image with the broker socket mounted
- Verifies the container runs with the expected user mapping and workspace mount
- Runs `ai-agent run` inside the container
- Confirms git and `gh` go through brokered auth
- Asserts missing socket wiring fails deterministically

If you already have a compatible image built, you can skip the build step by setting:

```bash
export AI_AGENT_READINESS_IMAGE=your-prebuilt-image
```

The harness expects Docker to be available locally and may need network access the first time it
builds the devcontainer image from `.devcontainer/Dockerfile`.
