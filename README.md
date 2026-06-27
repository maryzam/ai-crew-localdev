# AI Crew localdev

Linux-first GitHub credential broker and container foundation for running AI
coding agents with repo-scoped access.

AI Crew localdev provides Claude Code and Codex with brokered
GitHub credentials, repo-scoped sessions, fail-closed `git`/`gh` wrappers on the
intended command path, and a hardened generic devcontainer. It does not yet
provide a complete persistent development workspace, end-to-end observability,
or autonomous workflow management.

The north star is an autonomous, efficient, adaptive local dev environment: agents work inside governed project flows, quality is enforced through executable contracts, and a meta-agent layer monitors cross-project efficiency, resource use, token spend, and recurring failure patterns.

## What this repo contains

- Container config for the devcontainer flow
- Broker, launcher, helper, and wrapper binaries
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

## Quick Start

From an installed checkout, use `ai-agent up` as the primary entrypoint:

```bash
git clone https://github.com/maryzam/ai-crew-localdev.git
cd ai-crew-localdev
make install

# Create and install a GitHub App for the agent, then start the workspace.
# On first run, ai-agent up offers to run guided setup and writes validated config.
ai-agent up --workspace "$HOME/github" --langfuse
```

Inside the devcontainer shell, run your agent through the governed session path:

```bash
# Sign in once when the agent CLI asks. Claude and Codex store personal
# login/config state in /home/dev, backed by the ai-agent-home volume.
ai-agent run --agent claude --repo /workspace/my-project -- claude
```

On later re-entry, the same `/home/dev` is mounted so the CLIs can reuse their
state. This is exercised with Codex's real login/status commands; live Claude
OAuth persistence is not automated yet. GitHub repo access is separate: `git`
and `gh` inside managed runs use brokered repo-scoped credentials. Do not run
`gh auth login` in the container.

Managed runs write local telemetry to
`~/.config/ai-agent/run-telemetry.jsonl`, rotated with one `.1` backup. Set
Langfuse API keys or an OTLP traces endpoint to export traces. Inspect local
history with `ai-agent runs list` and `ai-agent runs show <run-id>`.

Use `--project ~/github/my-project` when a repository owns its own
`.devcontainer`; ai-agent preserves that project environment and injects the
broker/toolchain overlay.

## Product Gaps

The repository currently provides a Linux GitHub-auth broker foundation, not a
complete daily development environment or the north-star autonomous control
plane. The prioritized blockers and claim boundaries are maintained in
[docs/gap-analysis.md](docs/gap-analysis.md).

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

The harness expects Podman to be available locally and may need network access the first time it
builds the devcontainer image from `.devcontainer/Dockerfile`.
