# AI Crew localdev

Multi-AI local dev environment setup for the brokered auth flow used by `ai-agent`.

## What this repo contains

- Container config for the devcontainer flow
- Broker, launcher, helper, and wrapper binaries
- Supporting scripts for readiness and local validation
- Docs and fixtures for the brokered session model

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
