# Devcontainer readiness checks

The devcontainer entrypoint is intentionally strict. Startup stops immediately if
the broker or workspace wiring is invalid instead of dropping you into a shell
that will fail later.

The entrypoint validates:

- `AI_AGENT_AUTH_SOCK` is set.
- The workspace mount exists and is writable.
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
