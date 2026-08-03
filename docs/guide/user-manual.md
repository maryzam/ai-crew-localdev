# AI Crew Localdev — User Manual

Run Claude Code or Codex against your GitHub repos **without ever handing them a credential**.

The agent gets a short-lived token, scoped to one repo, minted on demand by a broker that runs on your host. Your GitHub App private key stays in that broker process. The agent — and anything it spawns — never sees it.

**Start here**: do the [Quick Start](#quick-start), then read [How it works](#how-it-works) so you know why each step exists. Deeper detail lives in the docs listed at the [bottom](#where-to-go-next).

---

## Quick Start

### What you need

- Linux, with `git` and GitHub repos using credential-free HTTPS or SSH remotes
- **Podman** (preferred) or Docker
- **Node.js** — for the devcontainer CLI, which `ai-agent up` offers to install for you

### 1. Create a GitHub App (once)

The broker mints tokens from a GitHub App, so you need one before anything else.

**GitHub → Settings → Developer settings → GitHub Apps → New GitHub App**, then:

- Uncheck **Webhook → Active**
- Permissions: `Contents` **Read & write**, `Pull requests` **Read & write**, `Metadata` **Read-only**
- Create it, **generate a private key**, and download the `.pem`
- **Install** the app on the repos your agent should touch

Keep the key private:

```bash
mkdir -p ~/.config/ai-agent
mv ~/Downloads/*.private-key.pem ~/.config/ai-agent/claude-app-key.pem
chmod 600 ~/.config/ai-agent/claude-app-key.pem
```

### 2. Install

```bash
curl -fsSLO https://github.com/maryzam/ai-crew-localdev/releases/latest/download/install.sh
sh install.sh latest
```

That installs one self-contained binary to `~/.local/bin` (checksum-verified). No clone needed — the devcontainer definition ships inside the binary. To build from source instead: `git clone`, then `make install`.

### 3. Start everything

```bash
cd ~/github/my-project
ai-agent start
```

`ai-agent start` resolves the repository containing the current directory, creates or resumes its private checkout, mounts only that checkout at `/workspace`, and starts the configured agent. You can instead pass the repository path explicitly: `ai-agent start ~/github/my-project`.

On the first run there is no config yet, so `ai-agent start` offers guided setup. Accept it. It asks for the agent name (e.g. `claude`), the App ID, the path to your PEM, and a git author identity — then queries GitHub, lists the repos your App can reach, and lets you pick which ones this agent may access. It writes `identities.json` and `policy.json` for you and continues booting.

The source repository must have a committed `HEAD`, a named branch, a credential-free GitHub HTTPS or SSH origin, and no staged, unstaged, conflicted, or untracked changes. The private checkout is populated locally without contacting the remote, canonicalizes its origin to credential-free HTTPS for brokered access, and shares no writable Git metadata or hardlinked objects with the human checkout.

### 4. Run an agent

The agent starts automatically. If several identities are configured, select one explicitly:

```bash
ai-agent start --agent codex -- --model o3
```

Everything after `--` is passed to the configured agent executable. The executable and repository flags are derived from the governed plan rather than repeated by the operator. Each private workspace is bound to that identity and tool; use `--new` when switching identities instead of mixing attribution in one result.

Sign in to Claude (or Codex) when it asks. That login is stored in `/home/dev`, a persistent volume, so you only do it once — it survives container restarts and even container replacement. It has nothing to do with GitHub access, which stays brokered.

Now let the agent work. Inside the session, `git push` and `gh pr create` authenticate on their own, against the repos you allowed and no others.

When the session finishes, its result remains private. Apply it only when the human checkout is still clean and unchanged from the recorded base:

```bash
ai-agent apply
```

If the human branch moved or has local changes, apply refuses without modifying it and retains the private workspace for manual integration or a later retry.

Use `ai-agent workspace list` to rediscover every retained workspace; corrupt or older metadata remains visible as `unreadable` instead of hiding the catalog. `ai-agent workspace remove <id>` reclaims one only after its run lock proves it is idle, and protects both uncommitted files and commits beyond the recorded base unless `--force` explicitly discards them. If the source checkout moved, select its retained ID explicitly with `ai-agent apply <new-path> --workspace <id>`; ai-agent accepts the move only when the old path is gone and the repository identity still matches.

**Do not run `gh auth login` in the container.** You don't need it, and the managed `gh` wrapper rejects it.

---

## How it works

### One `git push`, end to end

```
  agent runs:  git push origin main
       │
       ▼
  git needs a password, so it calls its configured credential helper
       │
       ▼
  ai-agent-credential-helper ──── unix socket ────► ai-agent-broker  (on your host)
       │                                                   │
       │                                    1. Is this session still alive?
       │                                    2. Is this repo in this agent's policy?
       │                                    3. Sign a JWT with the App key, exchange it
       │                                       for a GitHub installation token
       │                                       (~1 hour, this repo only)
       │                                    4. Append an audit line
       │                                                   │
       ◄──────────────── short-lived token ────────────────┘
       │
       ▼
  git pushes over HTTPS with that token — and never stores it

  Broker unreachable, session revoked, or repo not in policy?
       └──► git fails loudly. It never falls back to your personal credentials.
```

`gh` works the same way: in a managed session the only `gh` on `PATH` is a wrapper that clears any inherited `GH_TOKEN`, requests a fresh brokered token, and passes it only to the real `gh` child process.

### The trust boundary at a glance

The private key lives in one process on your host. The container the agent runs in only ever gets a socket and a short-lived token — never the key.

```mermaid
flowchart LR
    subgraph host["Your host — trusted zone"]
        PEM[["GitHub App private key<br/>never leaves this process"]]
        Broker["ai-agent-broker"]
        PEM --- Broker
    end
    subgraph box["Devcontainer — where the agent runs"]
        Agent["claude / codex"]
        Shims["git helper + gh wrapper"]
        Socket(["broker socket<br/>(mounted in)"])
        Agent --> Shims --> Socket
    end
    GitHub[["GitHub"]]
    Socket -->|"asks: a token for THIS repo"| Broker
    Broker -->|"short-lived, repo-scoped token"| Socket
    Broker -.->|"signs a JWT, exchanges it for a token"| GitHub
    Agent -->|"pushes over HTTPS with that token"| GitHub
```

Everything the agent can touch is inside the dashed-in container box: the workspace, the socket, and whatever token it was just handed. The key, the policy decision, and the audit log stay on the host side of the boundary, where the agent cannot reach them.

### Why it is built this way

**The private key lives in exactly one process.** A GitHub App PEM signs tokens for *every* repo the App is installed on. If an agent could read it, a stray `cat`, a prompt injection, or a bad `npm` postinstall could exfiltrate access to all of them, permanently. The broker holds the PEM; agents hold tokens that expire.

**Each session is bound to one repo.** An agent working on `repo-one` cannot push to `repo-two`, even though the same App can reach both. Blast radius is the repo you asked for, not your whole account.

**Tokens are short-lived and issued on demand.** A leaked token is worth about an hour, on one repo — not indefinite access.

**Everything fails closed.** Broker down, session expired, repo not in policy: git and `gh` return an error. There is deliberately no fallback to your personal credentials, because a silent fallback would quietly undo everything above.

**Ambient credentials are scrubbed at launch.** `GH_TOKEN`, `GITHUB_TOKEN`, `SSH_AUTH_SOCK`, `GIT_ASKPASS` and friends are stripped from the agent's environment, so the agent cannot "helpfully" reuse *your* credentials instead of asking the broker.

**Policy is enforced broker-side.** The shims are convenience, not security. Every decision is made by the broker, which the agent cannot patch, replace, or argue with.

**Every credential issued is audited.** Session, repo, and permissions land in `~/.config/ai-agent/audit.log`. You can always answer "what did it touch?"

**The container gets a socket, not a key.** Only the broker's Unix socket is mounted in. No PEM, no token, no `.git-credentials` ever enters the container filesystem.

### What `ai-agent start` actually does

1. Resolves one explicit clean source repository and records its named branch and exact base commit
2. Creates or resumes an owner-only, self-contained private checkout using local Git transport without hardlinks or remote access
3. Runs guided setup when needed, starts the broker, and validates host and container readiness
4. Builds or reuses the managed devcontainer and mounts only the private checkout at `/workspace`
5. Starts the selected agent through the governed `ai-agent run` path, then preserves its result for explicit application

### Where things end up

| Path | What it is |
|------|------------|
| `~/.config/ai-agent/identities.json` | Who each agent is: App ID, PEM path, git author |
| `~/.config/ai-agent/policy.json` | What each agent may touch: repos and permissions |
| `~/.config/ai-agent/*.pem` | Your GitHub App key. Host only, mode `600`. |
| `~/.config/ai-agent/audit.log` | Every session and credential issued |
| `~/.config/ai-agent/run-telemetry.jsonl` | Local run history: tokens, duration, verification results |
| `~/.local/share/ai-agent/workspaces/` | Owner-only private repository workspaces and durable result metadata |
| `~/.local/share/ai-agent/devcontainer/` | Generated managed-container build contexts |
| `/home/dev` in the container (`ai-agent-home` volume) | Claude/Codex logins and config — persists across restarts |
| `/workspace` in the container | One private session checkout; the human source checkout and sibling repositories are not mounted |

### What this does *not* protect against

Being honest about the edges, so you don't over-trust it:

<!-- BEGIN generated: security-non-goals (regenerate with `make security-claims`) -->
- **Single-user workstation only.** A fully compromised user account or operating system can reach the broker socket; this is a single-user workstation tool, not a multi-tenant service.
- **Not adversarial process containment.** The tool protects brokered credentials on the managed path; it does not turn the container into a general-purpose sandbox for hostile local processes, arbitrary network access, or files exposed by your workspace or custom image.
- **Managed commands only.** Use managed `git` and `gh` commands for repository access; commands that intentionally bypass the managed path are outside the credential guarantees.
- **HTTPS remotes only.** SSH git remotes are unsupported; use HTTPS remotes.
- **Linux only.** Non-Linux hosts are not supported yet.
- **Agent login-state checks are local.** `ai-agent up` can recognize saved agent CLI login state, but that local check is not a provider-backed authenticated request.
<!-- END generated: security-non-goals -->

---

## Everyday use

Re-enter through `ai-agent start`; it resolves the same source repository and resumes its active private workspace. Use `--new` only when you deliberately want another isolated result from the current committed source state. The command prints every workspace ID, so an older retained result remains addressable with `ai-agent apply --workspace <id>`.

| I want to… | Command |
|------------|---------|
| Start or resume a session | `ai-agent start` |
| Start another isolated session | `ai-agent start --new` |
| Apply the active result | `ai-agent apply` |
| Apply an older retained result | `ai-agent apply --workspace <id>` |
| Check my setup | `ai-agent doctor` (add `--mode container` for container prerequisites) |
| See who is signed in | `ai-agent auth status` (inside the container) |
| See what agents have been doing | `ai-agent runs list`, then `ai-agent runs show <run-id>` |
| List / kill sessions | `ai-agent session list`, `ai-agent session revoke <id>` |
| Allow a new repo | Re-run `ai-agent setup`, or edit `policy.json` and restart the broker |
| Rebuild the managed container image | `ai-agent start --build` |

When something misbehaves, run `ai-agent doctor` first — it names the broken check and the fix. The most common ones:

| Symptom | Fix |
|---------|-----|
| `resource_not_allowed` | The repo is not in this agent's `policy.json` — add it and restart the broker |
| `failed to create session` | Broker is not running: `systemctl --user start ai-agent-broker.socket` |
| `SSH remote not supported` | `git remote set-url origin https://github.com/owner/repo.git` |
| `no agent command specified` | You forgot the `--` before the agent command |

Full tables in [Troubleshooting](troubleshooting.md).

---

## Two things worth turning on next

### Use your project's own devcontainer

The generic container ships Go, Node, and Python. If your project needs Ruby, Postgres, and Redis, don't fight it — point `ai-agent` at the project's own `.devcontainer` instead:

```bash
ai-agent up --project ~/github/my-rails-app
```

Your project's features, Compose services, forwarded ports, and `postCreate` all run as they normally would. `ai-agent` just injects the broker overlay: the socket, the shims, and a `gh` that resolves to the broker wrapper.

### Make the agent prove its work

Drop a `.ai-agent/manifest.json` in a repo and every managed run in it executes your quality gates after the agent finishes — and re-launches the agent if they fail:

```json
{
  "schema_version": "ai-agent-manifest/v2",
  "contracts": [
    {"name": "tests", "command": "make test"},
    {"name": "lint", "command": "make lint", "retry": "never"}
  ]
}
```

No flags needed after that: `ai-agent run --agent claude --repo . -- claude` runs the agent, runs the contracts, retries the agent on failure (up to twice by default), and only reports success when the gates pass. Passing output stays hidden; failure output is bounded so a broken test suite cannot flood your terminal or the agent's context.

---

## Where to go next

| Doc | What's in it |
|-----|--------------|
| [Setup](setup.md) | Install, GitHub App, `identities.json`, `policy.json`, broker service, env vars, file locations |
| [CLI Reference](cli-reference.md) | Every command and flag |
| [Using the Container](using-the-container.md) | The container: image contents, agent login state, project mode, manual runs |
| [Quality Gates](quality-gates.md) | Manifest contracts, verify-and-retry, home isolation, token and output budgets |
| [Observability](observability.md) | Run history, Langfuse, the advisory analyzer, findings ledger |
| [Security — What Protects You](security-for-users.md) | What the tool guarantees about your credentials, and what it does not |
| [Troubleshooting](troubleshooting.md) | Symptom → fix |

Building the tool or contributing? The [design docs](../design/README.md) cover architecture, how the security guarantees are enforced, building from source, and design principles.
