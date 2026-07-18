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

Each invariant is backed by code; documentation alone is not enforcement.

| # | Invariant | Enforced by |
|---|-----------|-------------|
| 1 | Agent processes never have access to PEM files or signing primitives. | Key material is read and held only in the broker (`internal/providers/github/signer.go`); nothing mounts it into the container. |
| 2 | Tokens are short-lived (GitHub's ~1-hour installation token TTL). | The broker mints installation tokens per request; it never hands out the App JWT. |
| 3 | Each session is bound to one repository — cross-repo access is denied. | Session binding validated on every credential request against the requested resource. |
| 4 | Fail closed: when the broker is unreachable, `git` and `gh` fail explicitly. | The shims return errors and set `GIT_TERMINAL_PROMPT=0`; there is no ambient fallback path. |
| 5 | Ambient credentials are scrubbed from the session environment. | The launcher strips `GH_TOKEN`, `SSH_AUTH_SOCK`, `GIT_CONFIG_*`, and the rest before exec. |
| 6 | Every credential issuance is logged with session, repo, and permission set. | Broker audit write on each issuance, durable and atomic. |
| 7 | The broker validates the caller's UID via `SO_PEERCRED` on every connection. | Peer-credential check in the broker socket accept path. |
| 8 | Session binding secrets reach agents through sealed memfds, never env or disk. | Memfd sealed and passed by FD number; session management authorizes by peer UID. |
| 9 | Containers mount only the broker socket — no keys or PEM files enter. | Devcontainer mounts are the socket dir, the workspace, and the home volume only. |
| 10 | Policy is enforced broker-side, not in the shims. | The shims are convenience; the broker revalidates policy and is the authority. |
| 11 | The container runs with no capabilities, a read-only root, and no-new-privileges. | `runArgs` in the devcontainer definition; see [Using the Container](../guide/using-the-container.md). |
| 12 | PEM private keys must be owner-only. | `securefile.ReadOwnerOnly` refuses group/other-readable keys at load; `ai-agent doctor` fails `broker-pem-permissions` before a session starts. |
| 13 | Credential-writing `gh auth` commands are rejected on the supported path. | The `ai-agent-gh` wrapper blocks `login`/`setup-git`/`refresh` before requesting a credential. |
| 14 | Container and Langfuse assets come from the embedded copy by default; the ambient working directory is never executed. | Both the generic devcontainer and Langfuse resolve through `assetsource`, which gates checkout overrides behind an explicit `AI_AGENT_DEV_ASSETS_DIR`. A claim test plants a hostile `contrib/langfuse/docker-compose.yml` in the CWD and asserts it is ignored; an architecture guard fails if asset resolution reads `os.Getwd`. |
| 15 | Doctor's readiness verdict matches what the broker will actually load. | Doctor and the broker share `securefile` for owner-only validation; a claim test asserts doctor readiness equals broker acceptance across adversarial key fixtures, and an architecture guard forbids re-implementing the check in the readiness package. |

## Explicit Non-Goals

These are real and stated on purpose:

- **Single-user workstation only.** Same-UID processes share the broker socket.
- **Not adversarial process containment.** The supported path refuses host-native managed runs and keeps durable credentials behind the broker, but a process that can make raw network calls, reach absolute host paths made available by the workspace, or compromise the same UID is outside the containment claim.
- **The `gh` wrapper covers the supported command path, not a sandbox boundary.** A process that invokes a real `gh` by absolute path, or makes raw network calls, is not stopped by the wrapper.
- **HTTPS remotes only.** SSH git operations are not supported.
- **Linux only** (Phase 1).
- Login-state tests prove persistence and local recognition, not a provider-backed authenticated request.

## Hardening roadmap

These are the deliberate next steps beyond the current credential-containment claim. This section records the intent and the attack each step would close.

- **Supply-chain containment (npm / pip / postinstall).** A dependency install inside a run is the most likely adversary. Options under evaluation: default-deny network egress with an allowlist, pinned and integrity-checked package sources, and disallowing lifecycle scripts unless declared. Closes credential and data exfiltration through a malicious transitive dependency.
- **Real-tool removal / egress policy.** Remove or gate the absolute-path `gh` and constrain raw outbound network from the container namespace, so brokered auth is the only route to GitHub. Closes the absolute-path and raw-socket bypass in the explicit non-goals above.
- **Reproducible runtime.** Auditable base image, package, and fetched-artifact versions with integrity checks, so the container's contents are a known quantity. Tracked under the P2 supply-chain reproducibility gap.

Each item leaves the roadmap only when it is implemented on the supported path and validated by a check that fails if the behavior regresses — the same completion rule the gap analysis applies.
