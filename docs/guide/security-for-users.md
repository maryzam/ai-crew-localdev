# Security — What Protects You

**Scope: what this tool guarantees about your GitHub credentials, in plain English — what it saves you from, and what it does not.** For how those guarantees are enforced in code, the exact credential path, and where the hardening roadmap goes next, see [Security Design](../design/security-design.md).

## The one thing to understand

Your GitHub App private key is the crown jewel: it can mint access to *every* repository the App is installed on. The whole job of this tool is to make sure the agent — Claude, Codex, anything they spawn, anything a prompt injection or a malicious `npm` postinstall talks them into running — never sees that key.

Instead, a small **broker** process on your host holds the key. When the agent needs to push, the broker mints a token that is short-lived (about an hour), scoped to one repository, and useless anywhere else. The agent only ever holds that token.

## What it saves you from

- **A stolen private key.** The key never enters the container or the agent's reach, so a `cat`, a prompt injection, or a compromised dependency cannot exfiltrate durable access to all your repos.
- **Cross-repo blast radius.** A session is bound to one repository. An agent working on `repo-one` cannot push to `repo-two`, even though the same App can reach both.
- **Silent credential reuse.** `GH_TOKEN`, SSH agents, `.netrc` and friends are stripped from the session, so the agent cannot quietly fall back to *your* personal credentials instead of asking the broker.
- **A leaked token mattering much.** Any token the agent does hold expires in about an hour and only works on the one repo it was minted for.
- **"What did it touch?" being unanswerable.** Every credential the broker issues is written to an audit log with the session, repo, and permissions.

## The guarantees, in one table

| Guarantee | What it means for you |
|-----------|-----------------------|
| Key isolation | The GitHub App PEM stays in the host broker process. No key, token, or `.git-credentials` file ever enters the container. |
| Short-lived tokens | Credentials are GitHub installation tokens with a ~1-hour lifetime, minted on demand. |
| One repo per session | Each session is bound to a single repository; cross-repo pushes are denied. |
| Fail closed | If the broker is down, the session expired, or the repo is not in policy, `git` and `gh` return an error. There is deliberately no fallback to personal credentials. |
| Scrubbed environment | Ambient GitHub, SSH, and git-config credentials are removed before the agent starts. |
| Audited | Every issuance is logged to `~/.config/ai-agent/audit.log`. |
| Enforced key hygiene | The broker refuses to load a PEM that is group- or world-readable, and `ai-agent doctor` flags it (and past-due rotation) before a session starts. |
| `gh auth` blocked | The managed `gh` wrapper rejects credential-writing `gh auth` commands, so personal tokens are never written into the container. |

## What it does *not* protect against

Being honest about the edges, so you don't over-trust it:

- A **fully compromised user account or kernel.** Same-UID processes on your workstation can reach the broker socket; this is a single-user workstation tool, not a multi-tenant sandbox.
- **Adversarial process containment.** Managed runs require the devcontainer marker and durable credentials stay behind the broker, but spoofed markers, raw network calls, absolute paths made available by the workspace or a custom image, and same-UID compromise are outside the containment claim.
- **SSH git remotes** (unsupported — use HTTPS) and **non-Linux hosts** (not yet).

## Good habits

These are not enforced by the tool; they reduce blast radius further:

- Keep each agent's `resources` list as small as practical, and drop entries you have stopped using.
- Review `~/.config/ai-agent/audit.log` periodically.
- Revoke sessions you no longer need: `ai-agent session revoke <id>`.
- Rotate GitHub App PEM keys before they age out. `ai-agent doctor` warns once a key passes the reminder age.
- Prefer containerized sessions for isolation.

## Want the mechanics?

The exact credential handshake (sealed memfds, `SO_PEERCRED`, JWT exchange), the full list of enforced invariants with their code enforcement points, and the containment hardening roadmap live in [Security Design](../design/security-design.md).
