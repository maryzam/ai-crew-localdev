# Agent Login

**Scope: creating the broker-held subscription login for Claude Code and Codex with the greenfield `ai` command.** The legacy `ai-agent` commands are described in the [CLI Reference](cli-reference.md).

## `ai login`

```text
ai login <claude|codex>
```

`ai login` runs the pinned native login in a short-lived rootless Podman container and stores the result where only the host broker reads it. Task containers never mount it, so agents only ever see placeholder credentials. Run it from an interactive terminal; it refuses when standard input or output is not a terminal.

- **Claude** runs `claude setup-token`. Complete the browser sign-in it prints. The token it shows is captured from the terminal output, then verified with one request through the genuine Claude CLI before it is stored. A token that the CLI cannot use is never stored.
- **Codex** runs `codex login --device-auth` with file credential storage. Enter the device code it prints. The resulting ChatGPT login must contain an unexpired access token, a refresh token, and an account. The command also installs the Codex executable pinned for your CPU architecture, which the broker uses for token refresh instead of whatever `codex` is on your `PATH`.

The first run builds the pinned login image, which can take several minutes.

## What success means

Each successful login is stored as a new owner-only generation and is recorded, with its SHA-256, in the evidence journal before it takes effect. The command prints the generation it published:

```text
claude login published for the broker as generation 3f9c0a6e1b2d4c58
```

Superseded generations are removed after the new one is recorded. If anything fails before the record is written, including an abandoned sign-in, a rejected token, or a full disk, the previous login stays in force and the failure reason is recorded.

## Files

All paths are under `${XDG_DATA_HOME:-~/.local/share}/ai-crew`, and every directory is owner-only.

| Path | Contents |
|------|----------|
| `broker/login/<agent>/<generation>/` | The published credential: `token` for Claude, `auth.json` for Codex |
| `broker/bin/codex-<version>/codex` | The pinned Codex refresh executable |
| `evidence/custody.jsonl` | Login requests, failures with their reason, and publications with credential and image digests; never the credential itself |

A credential file edited or replaced by hand no longer matches its recorded digest, and the broker refuses it. Run `ai login` again instead.

## Limits

| Step | Budget |
|------|--------|
| Podman health check | 10 seconds |
| Login image build | 10 minutes |
| Native sign-in | 15 minutes, after which the container is stopped |
| Claude token verification | 2 minutes |
| Captured Claude output | 64 KiB |

Only one login runs at a time; a second `ai login` refuses while one is in progress.
