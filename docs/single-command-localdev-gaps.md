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

`origin/main` at `d986438` already provides important building blocks:

- a devcontainer image with `claude`, `codex`, and `gemini` preinstalled
- a non-root runtime user
- broker-socket-only exposure into the container
- fail-closed git and `gh` credential shims
- readiness checks for brokered auth behavior inside the image

This is a real containerized brokered-auth foundation.

It is not yet a single-command, preconfigured local dev product.

## Critical Gaps

### 1. No Single Supported Startup Command

The current flow is still multi-step:

1. ensure the host broker is running
2. export required host environment variables
3. launch the devcontainer
4. shell into the container
5. run `ai-agent run` inside the container

Why this matters:

- the environment is not actually “spin up with one command”
- startup remains easy to mis-sequence or misconfigure
- operator knowledge is still part of the bootstrap contract

### 2. The Environment Is Not Fully Preconfigured End to End

The image is preloaded with agent CLIs, but the working setup still depends on host-managed state:

- host broker socket
- host `XDG_RUNTIME_DIR`
- host `AI_AGENT_WORKSPACE`
- host identities file
- host policy file
- host PEM material
- host runtime tools such as container tooling and devcontainer integration

Why this matters:

- fresh-machine onboarding is not turnkey
- failure can happen before container launch
- the product is still closer to “container plus host prerequisites” than “ready-to-use local dev environment”

### 3. The Real User Workflow Is Under-Tested

The current end-to-end readiness validation proves the container image and brokered session path via direct Docker commands. It does not fully validate the actual user-facing devcontainer workflow and the full host/runtime wiring expected in normal use.

Why this matters:

- test coverage is stronger for the image than for the product workflow
- regressions in the supported launch path can slip through
- readiness claims are stronger than the current validation scope

### 4. Security Is Stronger for Secret Isolation Than for Runtime Confinement

The current design does the right high-level thing by keeping signing material on the host and exposing only the broker socket to the container.

However, the runtime hardening story still appears incomplete. Areas that need explicit decisions and enforcement include:

- dropped capabilities
- `no-new-privileges`
- read-only root filesystem where practical
- tight writable-mount policy
- seccomp and AppArmor/SELinux posture
- network posture and egress expectations

Why this matters:

- “secure environment” is broader than “keys are not mounted”
- hostile or compromised agent processes still need stronger containment

### 5. Build Reproducibility and Supply-Chain Control Are Not Tight Enough

The image currently depends on floating upstream inputs and unpinned global tool installs.

Why this matters:

- the environment can drift without repo changes
- reproducing a known-good build is harder
- the operational meaning of “preconfigured” becomes unstable over time

### 6. Runtime Expectations Are Inconsistent

The repo currently mixes different runtime expectations across docs, checks, and readiness validation.

Why this matters:

- the preferred runtime path is not as crisp as it should be
- debugging and support are harder
- confidence in the documented “default” flow is reduced

### 7. Platform Scope Is Not Yet Crisp

The implementation and architecture notes point most strongly at a Linux-first path. Cross-platform behavior remains less clearly validated.

Why this matters:

- support expectations are easy to overstate
- users on non-Linux hosts are more likely to encounter launch-time friction

## Bottom Line

The repo already has the core broker/session/container primitives needed for a secure local-dev platform.

What is still missing is the product layer that turns those primitives into a single-command, ready-to-use environment:

- one real startup command
- reduced host setup burden
- hardened runtime policy
- deterministic image inputs
- end-to-end tests for the exact supported workflow
- tighter docs and support boundaries

Until those gaps are closed, the most accurate description of the current state is:

> a solid containerized brokered-auth foundation, not yet a fully turnkey single-command local development environment
