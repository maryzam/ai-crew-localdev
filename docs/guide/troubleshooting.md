# Troubleshooting

**Scope: symptom → fix.** Start here when something breaks. Anything that needs explaining rather than fixing belongs in the doc that owns it.

## First: run the doctor

```bash
ai-agent doctor                    # host session prerequisites
ai-agent doctor --mode container   # container prerequisites
ai-agent doctor --json             # machine-readable
```

It names the failing check and its remediation. Most of the tables below are just the shortcuts.

## The broker won't start

```bash
systemctl --user status ai-agent-broker.service
journalctl --user -u ai-agent-broker.service --no-pager -n 50
```

Common causes: a bad PEM path in `identities.json`; an invalid policy (`ai-agent policy validate`); a stale socket that must be removed before restart.

## `ai-agent up` fails

| Symptom | Fix |
|---------|-----|
| `devcontainer CLI not found` | `npm install -g @devcontainers/cli`, or accept the auto-install prompt |
| `selected runtime podman is not ready` | Install Podman (`sudo apt-get install podman`), or rerun with `--runtime docker` |
| `prepare devcontainer: ...` | The binary could not stage its build context — check that `$AI_AGENT_DATA_DIR` (default `~/.local/share/ai-agent`) is writable |
| `broker did not become ready` | `journalctl --user -u ai-agent-broker -n 20` |
| `readiness checks failed` | `ai-agent doctor --mode container --runtime podman` for the detail |
| Container started but no shell | Re-enter with the exact command `ai-agent up` printed (its `--workspace-folder` is this workspace's own `~/.local/share/ai-agent/devcontainer/<id>`) |
| Container build fails | Ensure Podman or Docker is installed and running |

## A session won't launch

| Symptom | Fix |
|---------|-----|
| `failed to create session` | Broker not running → `systemctl --user start ai-agent-broker.socket` |
| `credential helper not found` | Run `make install`; ensure `~/.local/bin` is on `PATH` |
| `no agent command specified` | You forgot the `--` separator |
| `resource_not_allowed` | Add `github:repo:owner/name` to `agents.<name>.resources` in `policy.json`, then reload the broker |
| `SSH remote not supported` | `git remote set-url origin https://github.com/owner/repo.git` |

## git or gh fails inside a session

| Error | Cause | Fix |
|-------|-------|-----|
| `session_not_found` | Session expired or was revoked | Re-launch with `ai-agent run` |
| `resource_not_allowed` | Resource not bound to this session, or not in the agent's policy | Check `AI_AGENT_SESSION_REPO` against the session's resource; verify the policy lists it for this agent |
| `unknown_credential_type` | Wrong credential type for the resource | Use the type that serves this resource's provider (`github_app_installation` for `github:repo:*`) |
| `binding_mismatch` | Corrupted binding | Re-launch the session |
| `connection refused` | Broker down | Restart the broker |
| `rate_limited` | Too many credential requests | Wait and retry |

## Container problems

| Symptom | Fix |
|---------|-----|
| `broker socket not found` | Start the host broker; verify `$XDG_RUNTIME_DIR` is set |
| Permission denied on the socket | Ensure `--userns=keep-id` is in the Podman flags |
| `/workspace` is empty | Set `AI_AGENT_WORKSPACE` on the host before starting the container |
| Home not writable, logins not saved | `ai-agent-home` volume ownership must match `--userns=keep-id`; recreate the volume |
| Can't build the image | Ensure Podman (or Docker) and buildah are installed |

## Diagnostic checklist

```bash
ai-agent doctor
ai-agent doctor --mode container
ls -la ~/.config/ai-agent/{identities,policy}.json
ai-agent policy validate
ls -la ~/.config/ai-agent/*.pem               # should be 600
ls -la $XDG_RUNTIME_DIR/ai-agent/broker.sock
systemctl --user status ai-agent-broker.socket
git -C ~/my-repo remote get-url origin        # must start with https://
env | grep -E "GH_TOKEN|GITHUB_TOKEN|SSH_AUTH_SOCK"
tail -20 ~/.config/ai-agent/audit.log
```
