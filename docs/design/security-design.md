# Security Design

**Scope (builder track): how the broker enforces zero trust in code, the exact credential path, the enforced invariants and where each is enforced, and the containment hardening roadmap.** The plain-English user view is [Security — What Protects You](../guide/security-for-users.md). Container confinement settings are in [Using the Container](../guide/using-the-container.md).

## Threat model

This system protects against **AI agent processes exfiltrating or misusing brokered GitHub credentials on the supported managed-run path**. It does **not** provide adversarial process containment, and it does **not** protect against a fully compromised user account or kernel.

The asset is the GitHub App private key. It signs tokens for every repository the App is installed on, so an agent that can read it holds durable access to all of them. The broker keeps that key in one host process and hands agents only short-lived, repo-scoped tokens. Everything below exists to keep that boundary intact even when the agent is adversarial by proxy — a prompt injection, a malicious dependency, or a confused tool call.

## The credential path

### Git

1. Git invokes `ai-agent-credential-helper` through the git credential protocol.
2. The helper reads the session binding secret from the inherited sealed memfd (the FD number is in `AI_AGENT_SESSION_BIND_FD`).
3. It calls the broker over the Unix socket, requesting a credential for the bound repository.
4. The broker validates the session, signs a JWT with the App key, and exchanges it for a GitHub installation token.
5. The helper returns the token as `x-access-token` / `<token>` in git credential format.
6. Git uses the short-lived token for the HTTPS operation and does not store it.

Cross-repo operations are denied: you cannot push to a repository the session is not bound to.

### gh

The `ai-agent-gh` wrapper intercepts every `gh` invocation:

1. Clears `GH_TOKEN` / `GITHUB_TOKEN` from the environment.
2. Requests a fresh credential from the broker.
3. Sets `GH_TOKEN` for the real `gh` child process only.
4. Execs the real `gh` binary.

In the devcontainer the only `gh` on `PATH` is the wrapper. The real binary is moved to `/opt/ai-agent/bin/gh` (`$AI_AGENT_REAL_GH`) so an agent cannot reach it by typing `gh`. The wrapper also rejects credential-writing `gh auth` commands (`login`, `setup-git`, `refresh`) before requesting a broker credential, so personal tokens are never written into the durable agent home.

## Enforced invariants and where they are enforced

Each invariant is generated from `internal/quality/securityclaims` and carries executable proof references. Documentation alone is not enforcement.

<!-- BEGIN generated: security-invariants (regenerate with `make security-claims`) -->
| # | Invariant | Enforced by |
|---|-----------|-------------|
| 1 | Durable provider secrets stay in broker/provider-owned code and are not returned through workspace credential APIs. | GitHub signing is provider-side, Langfuse egress uses durable keys only in the provider, and telemetry/session wire contracts omit provider secret fields. Proof: `internal/providers/github/signer_test.go:TestSignJWT`, `internal/providers/langfuse/provider_test.go:TestProviderPublishesWithDurableSecretOnlyInUpstreamAuthorization`, `internal/broker/api/api_contract_test.go:TestPublishTelemetryWireShapeHasNoProviderCredentialFields`. |
| 2 | GitHub credentials minted for a run are installation tokens scoped by repository and requested permissions. | The GitHub provider validates permission subsets, rejects escalation, and delegates short-lived installation-token minting to GitHub. Proof: `internal/providers/github/provider_test.go:TestProviderMintDownscope`, `internal/providers/github/provider_test.go:TestProviderMintRejectsEscalation`. |
| 3 | Each broker session is bound to declared resources, and cross-resource credential requests are denied. | Session creation records parsed resources and credential minting rechecks the requested resource against the session before provider work begins. Proof: `internal/broker/core/session_resources_test.go:TestMemorySessionStoreCreateResources`, `internal/broker/core/server_mint_credential_test.go:TestBrokerMintCredentialResourceNotInSession`. |
| 4 | Brokered git and gh paths fail closed instead of falling back to ambient personal credentials. | The launcher forces non-interactive git credentials, installs only the broker credential helper for git, and wrappers require managed-session state. Proof: `internal/runtime/launcher/scrub_invariants_test.go:TestScrubEnvDisablesInteractiveGitCredentials`, `internal/runtime/launcher/scrub_invariants_test.go:TestScrubEnvUsesOnlyBrokerCredentialHelper`, `test/e2e/project_devcontainer_test.go:TestProjectDevcontainerE2E`. |
| 5 | Ambient provider credentials are scrubbed from every managed agent process. | Provider interception profiles declare credential environment names and prefixes, and the launcher applies the union before exec. Proof: `internal/runtime/launcher/scrub_invariants_test.go:TestEveryProfileScrubsItsAmbientCredentials`, `test/e2e/project_devcontainer_test.go:TestProjectDevcontainerE2E`. |
| 6 | Credential issuance is withheld unless durable audit intent is recorded. | The broker writes audit records synchronously before credential mint success and latches storage failure into broker health. Proof: `internal/broker/core/server_audit_test.go:TestBrokerDoesNotMintWithoutDurableAuditIntent`, `internal/broker/core/fileaudit_test.go:TestFileAuditLoggerPersistsBeforeRecordReturns`. |
| 7 | The broker validates Unix peer credentials for every socket connection. | The accept path reads `SO_PEERCRED` before decoding a request and rejects peers outside the allowed UID boundary. Proof: `internal/broker/core/peercred_test.go:TestPeerCred`. |
| 8 | Token minting requires the per-session binding secret carried by sealed memfd, not environment or disk state. | The launcher creates a sealed bind fd, passes it to the child as fd 3, and the broker validates the secret on credential and telemetry requests. Proof: `internal/runtime/launcher/memfd_test.go:TestCreateBindFDIsSealed`, `internal/runtime/launcher/launcher_test.go:TestLaunchPassesBindFDToAgent`, `internal/broker/core/server_mint_credential_test.go:TestBrokerMintCredentialBindingMismatch`. |
| 9 | Generic devcontainers mount only the workspace, broker socket directory, and persistent agent home. | The checked-in generic devcontainer asset is the embedded release asset and its mount list is parity-checked. Proof: `internal/runtime/devcontainer/assets/assets_test.go:TestEmbeddedGenericAssetsMatchCheckout`, `internal/runtime/devcontainer/assets/assets_test.go:TestGenericDevcontainerDeclaresOnlyManagedMounts`. |
| 10 | Broker policy is the authority for provider resources; shims and manifests cannot grant credentials by themselves. | The planner performs broker-authoritative resource preflight and the broker reauthorizes resources at session creation and credential mint. Proof: `internal/control/planner_test.go:TestPlannerIncludesManifestResourcesAndResourceBudgets`, `internal/broker/core/server_test.go:TestBrokerCreateSessionDisallowedResource`, `internal/broker/core/server_safety_test.go:TestBrokerMintAfterReloadRemovingResourceIsRejected`. |
| 11 | The generic devcontainer declares dropped capabilities, no-new-privileges, and a read-only root filesystem. | The devcontainer runtime args are checked in the canonical asset and parity-checked against the embedded release asset. Proof: `internal/runtime/devcontainer/assets/assets_test.go:TestGenericDevcontainerDeclaresConfinementArgs`, `internal/runtime/devcontainer/assets/assets_test.go:TestEmbeddedGenericAssetsMatchCheckout`. |
| 12 | PEM private keys must be owner-only regular files before the broker will load them. | Doctor and broker loading share `securefile` rather than reimplementing PEM file validation. Proof: `internal/app/readiness/securefile_parity_test.go:TestDoctorPEMVerdictMatchesBrokerAcceptance`, `internal/quality/boundaries/boundaries_test.go:TestReadinessDefersSecureFileValidation`. |
| 13 | Credential-writing `gh auth` commands are rejected on the supported brokered path. | `ai-agent-gh` blocks login, setup-git, and refresh before requesting a broker credential or invoking real gh. Proof: `internal/shim/ghwrapper/ghwrapper_test.go:TestRejectPersistentAuthCommand`, `test/e2e/project_devcontainer_test.go:TestProjectDevcontainerE2E`. |
| 14 | Runtime assets come from embedded trusted sources by default, not the ambient working directory. | Generic devcontainer and Langfuse staging resolve through explicit asset sources, with checkout overrides gated behind a development environment variable. Proof: `internal/quality/boundaries/boundaries_test.go:TestAssetResolutionNeverTrustsWorkingDirectory`, `internal/runtime/devcontainer/assets/assets_test.go:TestGenericImageBuildsFromStagedBinaryNotSource`. |
| 15 | Doctor readiness reports the same PEM acceptance boundary the broker will enforce. | Readiness delegates to `securefile`, and parity tests compare doctor verdicts with broker acceptance across adversarial fixtures. Proof: `internal/app/readiness/securefile_parity_test.go:TestDoctorPEMVerdictMatchesBrokerAcceptance`, `internal/quality/boundaries/boundaries_test.go:TestReadinessDefersSecureFileValidation`. |
| 16 | Accidental host-native managed runs are rejected before brokered work begins on the current supported path. | The planner and launcher call the shared managed-runtime guard before helper resolution, broker setup, or launch side effects, and a boundary test prevents new direct marker readers; this is an operator guardrail, not a kernel identity boundary. Proof: `internal/platform/runenv/runenv_test.go:TestRequireManagedContainerRejectsMissingMarker`, `internal/quality/boundaries/boundaries_test.go:TestManagedRuntimeMarkerHasOneReader`, `internal/control/planner_test.go:TestPlannerRejectsNativeHostRunBeforeHelperResolution`, `internal/runtime/launcher/launcher_test.go:TestLaunchRejectsMissingDevcontainerMarkerBeforeBroker`. |
<!-- END generated: security-invariants -->

## Explicit Non-Goals

These are real and stated on purpose:

<!-- BEGIN generated: security-non-goals (regenerate with `make security-claims`) -->
- **Single-user workstation only.** Same-UID processes on the workstation can reach the broker socket; this is not a multi-tenant sandbox.
- **Not adversarial process containment.** The supported path rejects accidental host-native managed runs and keeps durable credentials behind the broker, but a process that can spoof the devcontainer marker, make raw network calls, reach absolute host paths made available by the workspace or a custom image, or compromise the same UID is outside the containment claim.
- **The `gh` wrapper covers the supported command path, not a sandbox boundary.** A process that invokes a real `gh` by absolute path, or makes raw network calls, is not stopped by the wrapper.
- **HTTPS remotes only.** SSH git operations are not supported.
- **Linux only.** Phase 1 supports Linux hosts.
- **Agent login-state checks are local.** `ai-agent up` login status and login-state tests prove persistence and local recognition, not a provider-backed authenticated request.
<!-- END generated: security-non-goals -->

## Hardening roadmap

These are the deliberate next steps beyond the current credential-containment claim. This section records the intent and the attack each step would close.

- **Supply-chain containment (npm / pip / postinstall).** A dependency install inside a run is the most likely adversary. Options under evaluation: default-deny network egress with an allowlist, pinned and integrity-checked package sources, and disallowing lifecycle scripts unless declared. Closes credential and data exfiltration through a malicious transitive dependency.
- **Managed-runtime identity.** Replace the devcontainer marker with an unforgeable runtime boundary: either broker authorization by distinct container peer UID with idmapped workspace and socket mounts, or a pathless connected-fd capability passed into the runtime with explicit broker-restart semantics.
- **Real-tool removal / egress policy.** Remove or gate the absolute-path `gh` and constrain raw outbound network from the container namespace, so brokered auth is the only route to GitHub. Closes the absolute-path and raw-socket bypass in the explicit non-goals above.
- **Reproducible runtime.** Auditable base image, package, and fetched-artifact versions with integrity checks, so the container's contents are a known quantity. Tracked under the P2 supply-chain reproducibility gap.

Each item leaves the roadmap only when it is implemented on the supported path and validated by a check that fails if the behavior regresses — the same completion rule the gap analysis applies.
