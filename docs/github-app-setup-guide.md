# GitHub App Setup Guide

This guide walks through creating and configuring GitHub Apps for use with the
`ai-agent` auth broker. Each AI CLI agent (Claude Code, Codex, Gemini CLI)
operates under its own GitHub App identity.

For the full architecture and security model, see
[ai-agent-auth-architecture.md](ai-agent-auth-architecture.md).

---

## 1. Overview

### Why one GitHub App per agent

- **Identity attribution.** Every commit and PR is tied to a specific bot
  identity (`claude[bot]`, `codex[bot]`, etc.), making the audit trail clear.
- **Independent revocation.** You can disable one agent's access without
  affecting the others.
- **Scoped permissions.** Each App can be installed on different sets of repos.

### How the broker uses GitHub Apps

```text
broker loads PEM at startup
  -> signs a JWT with the App's private key
  -> exchanges JWT for a short-lived installation token (1 hour)
  -> scopes the token to the requested repo and permissions
  -> returns the token to the credential helper or gh wrapper
```

The broker auto-discovers installation IDs by listing the App's installations
via the JWT. You can optionally override the installation ID in `policy.json`.

### Configuration files

| File | Purpose |
|------|---------|
| `~/.config/ai-agent/identities.json` | Maps agent names to GitHub App credentials and git identity |
| `~/.config/ai-agent/policy.json` | Controls which repos each agent can access, permissions, and session timeouts |

---

## 2. Creating a GitHub App

Repeat these steps for each AI agent (e.g., once for Claude, once for Codex,
once for Gemini).

1. Go to **Settings > Developer settings > GitHub Apps > New GitHub App**.

2. Fill in the basics:
   - **GitHub App name:** Use the convention `<username>-<agent>`, for example
     `maryzam-claude`. This name becomes the App slug used in bot identities.
   - **Homepage URL:** Your repo URL or any placeholder (e.g.,
     `https://github.com/maryzam/ai-crew-localdev`).

3. **Webhook:** Uncheck **Active**. This is a local-only App with no webhook.

4. Set **Repository permissions:**

   | Permission | Access |
   |------------|--------|
   | Contents | Read & write |
   | Pull requests | Read & write |
   | Metadata | Read-only (granted automatically) |

   Leave all Organization and Account permissions empty.

5. Under **Where can this GitHub App be installed?**, select
   **Only on this account**.

6. Click **Create GitHub App**.

7. Note the **App ID** shown on the General settings page -- you will need it
   for `identities.json`.

---

## 3. Generating and Storing the Private Key

1. On the App's settings page, scroll to **Private keys** and click
   **Generate a private key**. Your browser downloads a `.pem` file.

2. Create the key directory and move the file into place:

   ```bash
   mkdir -p ~/.config/ai-agent/keys
   mv ~/Downloads/<app-name>.*.private-key.pem ~/.config/ai-agent/keys/<agent>.pem
   ```

   For example, for Claude:

   ```bash
   mv ~/Downloads/maryzam-claude.2026-03-11.private-key.pem \
      ~/.config/ai-agent/keys/claude.pem
   ```

3. Lock down file permissions:

   ```bash
   chmod 0600 ~/.config/ai-agent/keys/<agent>.pem
   ```

4. Verify:

   ```bash
   ls -la ~/.config/ai-agent/keys/
   # Should show -rw------- for each .pem file
   ```

The broker loads this PEM into memory at startup to sign JWTs. The key never
leaves the broker process.

---

## 4. Installing the App on Repositories

1. On the App's settings page, click **Install App** in the left sidebar.

2. Click **Install** next to your account.

3. Select **Only select repositories** and choose the repos the agent should
   access.

4. Click **Install**.

After installation, the browser redirects to a URL like:

```
https://github.com/settings/installations/12345678
```

The number at the end (`12345678`) is the **installation ID**. The broker
auto-discovers this at startup, but you can record it for use in `policy.json`
if you prefer explicit control.

You can change the repo selection later at any time without regenerating keys.

---

## 5. Configuring identities.json

Create or edit `~/.config/ai-agent/identities.json`:

```json
{
  "schema_version": "ai-agent-identities/v2",
  "agents": {
    "claude": {
      "git_name": "claude[bot]",
      "git_email": "2961625+maryzam-claude[bot]@users.noreply.github.com",
      "github_host": "github.com",
      "app_id": "2961625",
      "app_key": "~/.config/ai-agent/keys/claude.pem",
      "tool": "claude-code",
      "model": "claude-sonnet-4-6"
    }
  }
}
```

### Field reference

| Field | Description |
|-------|-------------|
| `git_name` | Git author name for commits. Convention: `<appslug>[bot]` |
| `git_email` | Git author email. Format: `<app_id>+<appslug>[bot]@users.noreply.github.com` |
| `github_host` | GitHub hostname. Use `github.com` for GitHub SaaS. |
| `app_id` | Found on the App's General settings page |
| `app_key` | Path to the PEM file. Tilde expansion is supported. |
| `tool` | The CLI tool name (e.g., `claude-code`, `codex-cli`, `gemini-cli`) |
| `model` | The model identifier used by the agent |

### Finding your git_email

The noreply email format is `<app_id>+<app_slug>[bot]@users.noreply.github.com`.
The App ID is a numeric ID on the App's General settings page. The App slug is
the lowercased, hyphenated version of the App name (e.g., `maryzam-claude`).

---

## 6. Configuring policy.json

### Generate an initial policy

```bash
ai-agent policy init
```

This reads `identities.json` and creates `~/.config/ai-agent/policy.json` with
default values.

### Edit the policy

```json
{
  "schema_version": "ai-agent-policy/v1",
  "default_session_ttl": "8h",
  "default_idle_timeout": "1h",
  "agents": {
    "claude": {
      "allowed_repos": [
        "maryzam/ai-crew-localdev",
        "maryzam/snowflake-songs"
      ],
      "default_permissions": {
        "contents": "write",
        "pull_requests": "write",
        "metadata": "read"
      }
    }
  }
}
```

### Field reference

| Field | Description |
|-------|-------------|
| Field | Scope | Description |
|-------|-------|-------------|
| `default_session_ttl` | Top-level | Maximum session duration (default: `8h`) |
| `default_idle_timeout` | Top-level | Session expires after this long with no token requests (default: `1h`) |
| `allowed_repos` | Per-agent | List of `owner/repo` strings the agent may access |
| `default_permissions` | Per-agent | Permissions requested when minting installation tokens. These are downscoped from what the App has installed. |
| `installation_id` | Per-agent | Optional. Override auto-discovery with an explicit installation ID. |

### Validate the policy

```bash
ai-agent policy validate
```

This checks the `policy.json` schema, duration fields, repo slug formatting,
and permission values. It does not cross-check `identities.json`.

---

## 7. Verifying the Setup

Run the pre-flight diagnostics for local configuration and host readiness:

```bash
ai-agent doctor
```

A healthy setup produces output like:

```
ai-agent doctor
  ✓ identities file loaded (3 agents)
  ✓ PEM file for claude (mode 0600)
  ✓ PEM file for codex (mode 0600)
  ✓ PEM file for gemini (mode 0600)
  ✓ app_id configured for all agents
  ✓ policy file valid (3 agents)
  ✓ broker socket directory writable
  ✓ systemd --user available
  ✓ all agents have allowed_repos configured

9 passed, 0 warning, 0 failed
```

Fix any reported issues before running agent sessions. See the
[Troubleshooting](#10-troubleshooting) section for common fixes.

`ai-agent doctor` does not contact the GitHub API. App installation discovery
and repo access are exercised later when the broker mints a token for a real
git or `gh` operation.

---

## 8. Repeating for Additional Agents

Each AI agent needs its own:

1. GitHub App (created in the GitHub UI)
2. Private key (`.pem` file in `~/.config/ai-agent/keys/`)
3. Entry in `identities.json`
4. Entry in `policy.json`

Follow sections 2 through 6 for each additional agent. Run `ai-agent doctor`
after adding each one to verify.

---

## 9. Security Notes

**PEM files are the root of trust.**

- Keep them at `0600` permissions, owned by your user.
- Never commit PEM files to version control. Add `*.pem` to `.gitignore`.
- The broker is the only process that reads PEM files. Agent processes and
  containers never have access.

**Installation tokens are short-lived.**

- Tokens expire after 1 hour and are scoped to the repos selected during
  App installation.
- The broker uses HTTPS-only for all GitHub operations in managed sessions.

**Revoking access:**

- To remove access to a specific repo: edit the App's installation settings
  and deselect the repo.
- To revoke an agent entirely: uninstall the App, or delete it from
  Developer settings.

**Rotating keys:**

1. On the App's settings page, generate a new private key.
2. Replace the local PEM file at `~/.config/ai-agent/keys/<agent>.pem`.
3. Set permissions: `chmod 0600 ~/.config/ai-agent/keys/<agent>.pem`.
4. Restart the broker (or wait for socket activation to pick it up).
5. The old key is automatically invalidated once removed from the App settings.

---

## 10. Troubleshooting

| Error | Cause | Fix |
|-------|-------|-----|
| PEM file not found | Path in `identities.json` does not match the file on disk | Verify the `app_key` path. Run `ai-agent doctor`. |
| app_id missing or invalid | `app_id` not set in `identities.json` | Find the App ID on the App's General settings page in GitHub. |
| Permission denied on PEM file | File permissions are too open or too restrictive | Run `chmod 0600 ~/.config/ai-agent/keys/<agent>.pem` |
| No installations found | The App is not installed on any repos | Go to the App's settings, click Install App, and install it on at least one repo. |
| Repo not in allowed_repos | `policy.json` does not include the target repo | Add the `owner/repo` to the agent's `allowed_repos` list and run `ai-agent policy validate`. |
| JWT signing failed | PEM file is corrupted or not in PKCS#1/PKCS#8 format | Re-download the private key from the App's settings page. |
| Broker socket not found | Broker is not running | Start a session with `ai-agent run` (triggers socket activation) or check `ai-agent doctor`. |
