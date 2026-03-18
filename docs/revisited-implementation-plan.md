# Revisited Implementation Plan: Secure Turnkey Containerized Local Dev

## Goal

Deliver a secure containerized local development environment for `ai-agent` that:

- starts from a single supported command
- preinstalls the required agent tooling
- requires minimal host-side manual setup
- preserves the brokered-auth trust boundary
- is validated end to end using the same launch path users are expected to run

## Target State

The intended target is a developer experience where a user can move from a fresh host setup to a working managed session with one supported entrypoint. The environment should:

- build or fetch the container image deterministically
- verify host prerequisites before launch
- provision or validate broker config explicitly
- launch the container using the real supported runtime path
- start or attach to a managed session without requiring extra shell choreography
- keep PEM material and broker signing internals on the host
- apply meaningful container hardening beyond non-root execution

## Current State Summary

`origin/main` at `d986438` has a working brokered-auth container foundation:

- container image with preinstalled `claude`, `codex`, and `gemini`
- non-root runtime user
- broker-socket-only trust boundary for container sessions
- fail-closed `gh` wrapper and credential helper flow
- environment scrubbing for ambient GitHub and SSH credentials
- Docker-based readiness test that validates brokered git and `gh` access inside the image

What it does not yet provide is a turnkey secure local-dev product. The current supported workflow remains:

1. configure the host broker
2. export required host environment variables
3. launch the devcontainer
4. shell into the container
5. run `ai-agent run` inside the container

That is a valid implementation phase, but it does not satisfy the target of a single-command, ready-to-use local development environment.

## Findings

### 1. No single-command bootstrap

There is no supported wrapper or top-level command that gets a user from zero to running environment in one step. The current implementation explicitly depends on a multi-step container-first workflow.

Impact:

- onboarding friction remains high
- the supported path is easy to misconfigure
- the “ready to use” claim is materially overstated if interpreted as end-to-end setup

### 2. Heavy host-side prerequisites remain

The environment still depends on host-managed broker state and host configuration:

- live broker socket
- `XDG_RUNTIME_DIR`
- `AI_AGENT_WORKSPACE`
- host identities file
- host policy file
- readable host PEM files
- host container tooling

Impact:

- fresh-machine setup is not turnkey
- failure modes occur before the container even starts
- portability across developer machines is weak

### 3. Real supported launch path is under-tested

The end-to-end integration test exercises `docker build` and `docker run` directly. It does not validate the actual supported `devcontainer up` flow, `${localEnv:...}` interpolation, or the rootless Podman path described in docs and architecture notes.

Impact:

- the image is tested more than the product workflow
- regressions in the real launch path can land undetected
- confidence in developer experience is lower than test status suggests

### 4. Security posture is incomplete

The current design correctly keeps signing material out of the container, but runtime isolation is still light. The container currently relies mainly on non-root execution and user namespace mapping.

Missing or unverified hardening areas include:

- dropped Linux capabilities
- `no-new-privileges`
- read-only root filesystem where practical
- explicit writable mount strategy
- seccomp and AppArmor/SELinux posture
- network restrictions or a documented rationale for unrestricted network access

Impact:

- the trust boundary is improved, but not yet strong enough for “secure environment” language
- hostile or compromised tool processes still have a broad local execution surface

### 5. Supply-chain reproducibility is weak

The container build currently relies on floating base images and unpinned global npm package installs for the agent CLIs.

Impact:

- builds drift over time
- provenance is harder to audit
- breakage can appear without source changes in this repo

### 6. Runtime story is inconsistent

Container readiness checks require `podman` and `devcontainer`, while the documented readiness harness depends on Docker and the integration test validates the Docker path. That leaves the nominally preferred runtime path less proven than the fallback.

Impact:

- operator expectations are unclear
- troubleshooting guidance is harder to trust
- platform support statements are weaker than they should be

### 7. Platform coverage is incomplete

The architecture notes already acknowledge macOS and Windows Podman-machine caveats. The current implementation does not appear to provide platform-specific validation or supported-path guarantees there.

Impact:

- cross-platform portability remains aspirational
- launch-time errors are likely to be confusing outside Linux

## Revisited Plan

### Phase 1: Establish a Real Supported Bootstrap Path

Deliver a single supported entrypoint for environment startup. This can be a thin `ai-agent devenv up` wrapper, a `make up` target, or another explicit bootstrap command, but it must do real orchestration rather than defer the work to manual steps.

Requirements:

- validate host prerequisites before container launch
- create or validate runtime directories
- ensure the broker socket is reachable
- verify the workspace source path
- launch the environment through the canonical runtime path
- provide clear, actionable failures

Acceptance criteria:

- a documented one-command launch exists
- a new developer can reach a running container without manually sequencing setup steps
- the command is covered by automated tests

### Phase 2: Reduce Host Setup Burden

Make the environment closer to preconfigured by default.

Requirements:

- provide guided config bootstrap for identities and policy
- document exactly which secrets remain host-resident and why
- reduce reliance on manually exported environment variables where possible
- choose safe defaults for workspace discovery and runtime directory resolution

Acceptance criteria:

- `doctor` reports missing config with direct remediation
- common Linux setups do not require manual env export beyond exceptional cases
- host-side prerequisites are explicitly enumerated and minimized

### Phase 3: Harden the Container Runtime

Strengthen the container from “non-root with socket mount” to a deliberately hardened environment.

Requirements:

- drop unnecessary capabilities
- set `no-new-privileges`
- move toward read-only rootfs plus explicit writable mounts
- document SELinux/AppArmor expectations
- define the intended network posture
- audit writable paths needed by agent CLIs and only allow those

Acceptance criteria:

- runtime flags and mounts reflect the intended security posture
- the hardening is tested in CI or readiness automation
- docs stop overstating security beyond what the runtime enforces

### Phase 4: Make Builds Deterministic

Reduce supply-chain drift and improve reproducibility.

Requirements:

- pin base image versions or digests
- pin agent CLI versions
- avoid unnecessary live-install behavior during image build
- document upgrade policy for preinstalled tools

Acceptance criteria:

- image rebuilds are reproducible within a defined update policy
- breakage due to upstream floating dependencies is materially reduced

### Phase 5: Test the Actual Product Path

Align automated testing with the user-facing workflow.

Requirements:

- add end-to-end coverage for the canonical launch path, not just raw `docker run`
- test `devcontainer` integration if that remains the official mechanism
- test Podman-rootless if that remains the default runtime story
- keep the current brokered-auth assertions for git and `gh`

Acceptance criteria:

- the test suite validates the same path users are asked to run
- runtime-specific regressions are caught before merge

### Phase 6: Clarify Platform Support

Turn architecture caveats into explicit support policy.

Requirements:

- define supported host OS and runtime combinations
- add platform-specific validation where needed
- document unsupported or experimental paths clearly

Acceptance criteria:

- Linux support is explicit and tested
- macOS and Windows are either validated or clearly marked as not yet supported

## Recommended Execution Order

1. Build the real bootstrap command and wire it to current `doctor` checks.
2. Tighten docs so they describe the current system accurately.
3. Add runtime hardening and writable-path minimization.
4. Pin the container supply chain.
5. Replace “image-only readiness” with “real workflow readiness”.
6. Expand platform validation only after the Linux path is solid.

## Scope Recommendation for the Next PRs

### PR 1: Bootstrap and docs alignment

- add the supported one-command startup path
- update user docs to make that the only recommended flow
- keep existing container trust-boundary rules intact

### PR 2: Runtime hardening

- apply container security controls
- document and test writable paths and network assumptions

### PR 3: Reproducibility and runtime validation

- pin versions and image sources
- add end-to-end tests for the canonical runtime path

### PR 4: Platform policy

- document Linux as the hard-supported baseline
- gate or clarify macOS/Windows support

## Definition of Done

This effort should be considered complete only when all of the following are true:

- there is one documented command to start the environment
- the command works on the supported baseline platform from a clean machine
- required agent tooling is preinstalled in the launched environment
- broker secrets remain host-private
- the runtime applies explicit, tested hardening controls
- the end-to-end test suite validates the same launch path users follow
- documentation describes the actual guarantees and limitations precisely
