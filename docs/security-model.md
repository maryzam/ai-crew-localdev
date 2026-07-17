# Security Model

**Scope: what the broker protects, how it does it, and where the protection ends.** Threat model, the credential path for `git` and `gh`, enforced invariants, and honest limitations. Container confinement settings are in [Devcontainer](devcontainer.md).

## Threat model

This system protects against **AI agent processes exfiltrating or misusing GitHub credentials**. It does **not** protect against a fully compromised user account or kernel.

The asset is the GitHub App private key. It signs tokens for every repository the App is installed on, so an agent that can read it holds durable access to all of them. The broker keeps that key in one host process and hands agents only short-lived, repo-scoped tokens.

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

## Enforced invariants

1. Agent processes never have access to PEM files or signing primitives.
2. Tokens are short-lived (GitHub's 1-hour installation token TTL).
3. Each session is bound to one repository — cross-repo access is denied.
4. Fail-closed: when the broker is unreachable, `git` and `gh` fail explicitly rather than falling back to ambient credentials.
5. Ambient credentials (SSH keys, `gh auth`, `.netrc`) are scrubbed from the session environment.
6. Every credential issuance is logged with session, repository, and permission set.
7. The broker validates the caller's UID via `SO_PEERCRED` on every connection.
8. Session binding secrets reach agents through sealed memfds, never environment variables or disk. Session management (revoke, status) authorizes by peer UID.
9. Containers mount only the broker socket — no keys or PEM files enter the container.
10. Policy is enforced broker-side, not in the shims. The shims are convenience; the broker is the authority.
11. The container runs with no capabilities, a read-only root, and no-new-privileges.
12. PEM private keys must be owner-only. The broker refuses to load a key reachable by group or other, and `ai-agent doctor` fails the `broker-pem-permissions` check before a session starts.
13. Credential-writing `gh auth` commands (`login`, `setup-git`, `refresh`) are rejected by the wrapper on the supported path, so personal tokens are never written into the durable agent home. This blocks the supported command path, not raw or absolute-path invocations — see Limitations.

## Limitations

These are real and deliberate to state:

- **Single-user workstation only.** Same-UID processes share the broker socket.
- **The `gh` wrapper covers the supported command path, not containment.** A process that invokes `/opt/ai-agent/bin/gh` by absolute path, or makes raw network calls, is not stopped. This gap stays open until an end-to-end test proves brokered auth succeeds while ambient personal credentials are rejected — see [Product Gap Analysis](gap-analysis.md).
- **HTTPS remotes only.** SSH git operations are not supported.
- **Linux only** (Phase 1).
- Login-state tests prove persistence and local recognition, not a provider-backed authenticated request.

## Operational advice

These are not enforced by the tool. They are habits that reduce blast radius:

- Keep each agent's `resources` list as small as practical, and drop entries you have stopped using.
- Review `~/.config/ai-agent/audit.log` periodically.
- Revoke sessions you no longer need: `ai-agent session revoke <id>`.
- Rotate GitHub App PEM keys before they age out. `ai-agent doctor` warns via `broker-pem-rotation` once a key passes the reminder age.
- Prefer containerized sessions for isolation until always-containerized enforcement lands — see [Product Gap Analysis](gap-analysis.md).

PEM owner-only permissions and the `gh auth` write block used to live here as advice. They are now enforced invariants (12 and 13 above).
