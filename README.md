# AI Crew localdev

Linux-first governed workspace foundation for running AI coding agents with brokered provider access.

AI Crew localdev provides Claude Code and Codex with brokered GitHub credentials, repo-scoped sessions, fail-closed `git`/`gh` wrappers on the intended command path, managed devcontainer entry, bounded verification, local run history, native usage capture, live token budgets, and advisory adaptive findings. It does not yet provide complete project environment provisioning, a full operator cockpit, or autonomous workflow management.

The north star is an autonomous, efficient, adaptive local dev environment: agents work inside governed project flows, quality is enforced through executable contracts, and a meta-agent layer monitors cross-project efficiency, resource use, token spend, and recurring failure patterns.

## What this repo contains

- Container config for the devcontainer flow
- One multi-call `ai-agent` binary providing the CLI, broker daemon, and in-session shims by invocation name
- Supporting scripts for readiness and local validation
- Docs and fixtures for the brokered session model

## Current Foundation

| Capability | Current implementation |
|--------|---------------|
| Bootstrap | `ai-agent up` guides missing first-time configuration, starts the broker, checks prerequisites, launches the generic devcontainer, and opens a shell. |
| Local container | Host repositories are mounted into a Podman-first devcontainer with Docker fallback. |
| Brokered GitHub auth | GitHub App keys remain in the host broker; managed sessions request repo-scoped credentials on demand. |
| Security controls | The supported path scrubs ambient credentials, configures fail-closed wrappers, and runs the container with reduced privileges. |
| Verification | Unit, invariant, CI, and image-level readiness checks cover the credential broker foundation. |
| Adaptive efficiency | Managed Claude and Codex runs capture provider-reported usage through native OpenTelemetry even without remote export. `ai-agent runs analyze` produces bounded, advisory cross-project recommendations. |

## Quick Start

Install a released, checksum-verified single binary (Linux amd64/arm64), then use `ai-agent up` as the primary entrypoint:

```bash
curl -fsSLO https://github.com/maryzam/ai-crew-localdev/releases/latest/download/install.sh
sh install.sh latest

# Create and install a GitHub App for the agent, then start the workspace.
# On first run, ai-agent up offers to run guided setup and writes validated config.
ai-agent up --workspace "$HOME/github" --langfuse
```

The install script verifies the artifact against the release `SHA256SUMS` and refuses to install on any mismatch. The binary is self-contained: it carries the generic devcontainer definition and the Langfuse stack definition, stages them under `~/.local/share/ai-agent`, and installs the running binary into the container image, so `ai-agent up` needs no source checkout and runs from any directory. Installing from source still works:

```bash
git clone https://github.com/maryzam/ai-crew-localdev.git
cd ai-crew-localdev
make install
```

Full walkthrough and the reasoning behind each step: [docs/guide/user-manual.md](docs/guide/user-manual.md).

Inside the devcontainer shell, run your agent through the governed session path:

```bash
# Sign in once when the agent CLI asks. Claude and Codex store personal
# login/config state in /home/dev, backed by the ai-agent-home volume.
ai-agent run --agent claude --repo /workspace/my-project -- claude
```

On later re-entry, the same `/home/dev` is mounted so the CLIs reuse their state. Run `ai-agent auth status` inside the container to see who is signed in and how to remediate a missing login. Login-state persistence across container replacement is exercised for Codex's real login/status commands and for both offline Claude login paths (an `apiKeyHelper` and a persisted OAuth credentials file), verified by `claude auth status` and `ai-agent auth status`. These prove the login state persists and is recognized locally, not that a persisted credential authenticates against the provider; a live browser OAuth sign-in and refresh remain a manual first step. GitHub repo access is separate: `git` and `gh` inside managed runs use brokered repo-scoped credentials. Do not run `gh auth login` in the container.

Managed runs write local telemetry to `~/.config/ai-agent/run-telemetry.jsonl`, rotated with one `.1` backup. The launcher collects native Claude and Codex request usage through an authenticated loopback relay for local history. `ai-agent up --langfuse` additionally authorizes sanitized trace publication through the broker; backend keys remain inside the broker process. Inspect local history with `ai-agent runs list`, `ai-agent runs show <run-id>`, and `ai-agent runs analyze`.

Token fields come from provider-reported request events. Run history and Langfuse receive the same normalized values when remote export is enabled. Cost stays empty when a provider does not report it. The advisory analyzer reports coverage, repeated failures, retry waste, high-token runs, and missing verification without changing project files or policy.

Use `--project ~/github/my-project` when a repository owns its own `.devcontainer`; ai-agent preserves that project environment and injects the broker/toolchain overlay.

## Documentation

The docs are split by audience. If you are **running** the tool, stay in the guide. If you are **building or contributing** to it, the design track covers architecture, enforcement, and principles.

### For users — [docs/guide/](docs/guide/README.md)

| Doc | What's in it |
|-----|--------------|
| [User Manual](docs/guide/user-manual.md) | Start here: quick start, how the broker works, everyday commands |
| [Setup](docs/guide/setup.md) | Install, GitHub App, identities/policy, broker service, env vars |
| [CLI Reference](docs/guide/cli-reference.md) | Every command and flag |
| [Using the Container](docs/guide/using-the-container.md) | Image contents, agent login state, project mode, manual runs |
| [Quality Gates](docs/guide/quality-gates.md) | Manifest contracts, verify-and-retry, token/output budgets |
| [Observability](docs/guide/observability.md) | Run history, Langfuse, analyzer, findings ledger |
| [Security — What Protects You](docs/guide/security-for-users.md) | What the tool guarantees about your credentials, and what it does not |
| [Troubleshooting](docs/guide/troubleshooting.md) | Symptom → fix |

### For builders — [docs/design/](docs/design/README.md)

| Doc | What's in it |
|-----|--------------|
| [Architecture](docs/design/architecture.md) | Current and north-star architecture, domain ownership, core invariants |
| [Security Design](docs/design/security-design.md) | Credential path, enforced invariants and enforcement points, hardening roadmap |
| [Building From Source](docs/design/build-from-source.md) | `make build`/`install`, binary layout, embedded-asset contract, verify gates |
| [Design Principles](docs/design/design-principles.md) | Lean wrapper, invisible UX, quality-as-contract |
| [Gap Analysis](docs/design/gap-analysis.md) | What this does not do yet, and the claim boundaries |

## Product Gaps

The repository currently provides a Linux governed-agent workspace foundation, not a complete autonomous development environment. The remaining product gaps and claim boundaries are maintained in [docs/design/gap-analysis.md](docs/design/gap-analysis.md).

## Readiness Check

Run the devcontainer/container end-to-end readiness check with:

```bash
make readiness
```

That target runs `scripts/devcontainer-readiness.sh`, which launches the integration-tagged Go test [`test/e2e/devcontainer_readiness_test.go`](./test/e2e/devcontainer_readiness_test.go).

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

The harness expects Podman to be available locally and may need network access the first time it builds the devcontainer image from `.devcontainer/Dockerfile`. The image installs a prebuilt `bin/ai-agent` from the build context rather than compiling from source, so the harness runs `make build` first.

Two further targets prove the product journey beyond unit and readiness gates:

- `make journey` runs the clean-host journey in a fresh container with no source checkout: install the release artifact through `install.sh`, `ai-agent setup --non-interactive` against a mock GitHub API, broker start, `ai-agent doctor`, a managed run with a brokered push and default home isolation, a broker restart, and a second managed run. It runs automatically post-merge.
- `make e2e-live` is the single on-demand command that runs the full integration suite with real credentials: every readiness suite, the clean-host journey, then the live tests. Set `AI_AGENT_LIVE_REPO=owner/repo` (an operator-owned scratch repository) to exercise a real brokered push and PR create/close through `gh`, and add `AI_AGENT_LIVE_CLAUDE=1` to prove a provider-backed Claude request through persisted OAuth state inside a managed run. Live tests skip cleanly when the variables are unset; initial browser sign-in on a brand-new host remains a manual step.

When you edit `.devcontainer/` or `contrib/langfuse/`, mirror the change into the binary's embedded copies with `make devcontainer-assets` / `make langfuse-assets`. A test fails if they drift.
