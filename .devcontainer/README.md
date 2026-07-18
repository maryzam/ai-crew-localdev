# Devcontainer

## Build context contract

The image installs `bin/ai-agent` from the build context; it does not compile from a source tree. Run `make build` before building the image by hand or through VS Code.

These files are the canonical source for the generic devcontainer. `ai-agent` embeds a copy of them and stages it under `$AI_AGENT_DATA_DIR/devcontainer` on `ai-agent up`, which is what lets a released binary launch the container with no checkout present. After editing anything here, run `make devcontainer-assets` to update the embedded copy — a test fails if the two drift.

## Runtime configuration

`devcontainer.json` carries no inline comments; the rationale lives here.

- `runArgs` harden the container: `--userns=keep-id:uid=1000,gid=1000` maps the host UID/GID onto the fixed `dev` user for rootless Podman, `--cap-drop=ALL` and `--security-opt=no-new-privileges` remove ambient privilege, and `--read-only` plus a `noexec,nosuid` `/tmp` tmpfs keep the root filesystem immutable with a bounded writable scratch area.
- `updateRemoteUserUID` is `false` because Podman `keep-id` already maps the host UID onto the `dev` user; letting the CLI chown the home tree on top of that conflicts under rootless Podman.
- `workspaceMount` bind-mounts the host `AI_AGENT_WORKSPACE` directory at `/workspace`; export it on the host (`export AI_AGENT_WORKSPACE="$HOME/github"`) before launching.
- The `mounts` entries bind the host `ai-agent` runtime directory to `/run/ai-agent` so the broker socket path resolves inside the container namespace, and mount the `ai-agent-home` named volume at `/home/dev` so agent logins and CLI config survive restarts while everything else stays ephemeral.

## Readiness checks

The devcontainer entrypoint is intentionally strict. Startup stops immediately if the broker or workspace wiring is invalid instead of dropping you into a shell that will fail later.

The entrypoint validates:

- `AI_AGENT_AUTH_SOCK` is set.
- The workspace mount exists and is writable.
- The persistent home volume at `/home/dev` is writable and available for Claude/Codex login and config state across re-entry. Run `ai-agent auth status` to check each agent's login state and remediation.
- The broker path exists and is a Unix domain socket.
- The socket is owned by the current UID and remains owner-only.
- The socket accepts a live connection before the container command starts.

Typical host-side fixes:

```bash
export AI_AGENT_WORKSPACE="$HOME/github"
systemctl --user restart ai-agent-broker.socket
```

If the socket exists but is unusable inside a rootless container, re-check:

- `XDG_RUNTIME_DIR` on the host.
- `--userns=keep-id:uid=1000,gid=1000` in the devcontainer runtime args.
- SELinux relabeling requirements for your runtime, if applicable.

Do not persist personal GitHub CLI credentials in this home volume. Managed sessions get repo-scoped GitHub access from the host broker; credential-writing `gh auth` commands are rejected by the wrapper on the supported path.
