# Gap Summary: Single-Command Preconfigured Local Dev Environment

## Target

The target state is a secure local development environment for `ai-agent` that can be started with one supported command and is ready to use immediately with the key tooling already installed.

That target implies all of the following:

- one documented bootstrap entrypoint
- minimal host-side manual setup
- deterministic container/image provisioning
- clear runtime hardening
- end-to-end validation of the exact workflow users are expected to run
- docs that describe the real guarantees and limitations precisely

## Current State on `main`

As of March 2026, `origin/main` already provides important building blocks:

- a devcontainer image with `claude`, `codex`, and `gemini` preinstalled
- a non-root runtime user
- broker-socket-only exposure into the container
- fail-closed git and `gh` credential shims
- readiness checks for brokered auth behavior inside the image

This is a real containerized brokered-auth foundation.

## Gap Resolution Status

### 1. No Single Supported Bootstrap Experience — RESOLVED

**Resolution:** `ai-agent up` command (`internal/cli/up.go`) provides the single-command bootstrap:
- Resolves workspace and runtime directories
- Ensures broker is running (systemd socket activation → direct start fallback)
- Runs doctor checks programmatically
- Launches `devcontainer up` + interactive shell

### 2. The Real User Workflow Is Under-Tested — RESOLVED

**Resolution:** `TestDevcontainerCLIWorkflow` in `internal/e2e/devcontainer_cli_test.go` validates the real devcontainer CLI workflow end-to-end. Run via `make readiness-devcontainer`. The existing Docker-based integration test is retained for image-level validation.

### 3. Security Is Stronger for Secret Isolation Than for Runtime Confinement — RESOLVED

**Resolution:** `devcontainer.json` now includes runtime hardening:
- `--cap-drop=ALL` — no Linux capabilities
- `--security-opt=no-new-privileges` — prevents privilege escalation
- `--read-only` — immutable root filesystem
- `--tmpfs=/tmp:rw,noexec,nosuid,size=512m` — scratch space
- `--tmpfs=/home/dev:rw,nosuid,size=1g` — ephemeral user home
- Workspace and broker socket are the only writable bind mounts

### 4. Build Reproducibility and Supply-Chain Control Are Not Tight Enough — RESOLVED

**Resolution:** All upstream dependencies in `.devcontainer/Dockerfile` are now pinned:
- `golang:1.25.0` (was `golang:1`)
- `node:22.11.0-bookworm-slim` (was `node:22-bookworm-slim`)
- gh CLI installed from pinned `.deb` release (was floating apt repo)
- NPM packages pinned to specific versions
- `scripts/refresh-pins.sh` prints current latest versions for audit

### 5. Runtime Expectations Are Not Yet Aligned Cleanly — RESOLVED

**Resolution:** `ai-agent up` is the single supported entrypoint, eliminating the multi-step manual flow. Doctor checks run as a programmatic gate within `up`, not as a separate manual step. Environment variables are set automatically.

### 6. No CI — RESOLVED

**Resolution:** GitHub Actions workflows added:
- `.github/workflows/ci.yml` — build + test + lint on PR and push to main
- `.github/workflows/pr-tier.yml` — automatic T1/T2/T3 classification
- `.github/workflows/post-merge.yml` — post-merge smoke test

### 7. No Observability Layer — RESOLVED

**Resolution:** Self-hosted Langfuse v3 stack in `contrib/langfuse/`:
- 6-container docker-compose (web, worker, Postgres, ClickHouse, Redis, MinIO)
- All images pinned to specific versions
- `make langfuse-up` / `make langfuse-down` targets

### 8. No Executable Contracts — RESOLVED

**Resolution:** Invariant test files encode security and architecture contracts:
- `internal/launcher/scrub_invariants_test.go` — credential scrubbing, git config isolation
- `internal/launcher/memfd_invariants_test.go` — memfd sealing and round-trip integrity
- `internal/broker/session_invariants_test.go` — session lifecycle enforcement

## Future Consideration: Platform Scope

Platform scope should not be treated as a current gap for this phase. The project is explicitly Linux-only for Phase 1, which is a deliberate scoping choice rather than a shortcoming in the current implementation.

The real follow-up question is when, if ever, cross-platform support becomes a product goal beyond Phase 1.

Why this still matters later:

- support expectations will need explicit expansion if the project moves beyond Linux hosts
- cross-platform behavior should be validated only when that broader scope is intentionally adopted

## Bottom Line

All identified gaps have been addressed. The environment is now:

- **Single-command:** `ai-agent up` handles broker startup, readiness validation, container launch, and shell access
- **Hardened:** dropped capabilities, read-only root, no-new-privileges, ephemeral home
- **Reproducible:** all upstream dependencies pinned with version audit tooling
- **Tested:** invariant tests for security contracts, devcontainer CLI E2E test for the real workflow
- **Observable:** Langfuse stack for multi-agent session analytics
- **CI-gated:** build/test/lint on every PR, automatic tier classification, post-merge smoke
