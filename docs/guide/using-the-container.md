# Using the Container

**Scope: the container agents run in, from a user's point of view.** What is in the generic image, agent login state, project-aware mode, re-entering and stopping, and driving the container by hand. Command flags are in [CLI Reference](cli-reference.md); why the container is shaped this way is in [Security — What Protects You](security-for-users.md). How the image is built and the embedded-asset contract are in [Building From Source](../design/build-from-source.md).

## What gets staged

You do not build anything. The generic devcontainer definition ships **inside the `ai-agent` binary**; on `ai-agent up` it stages a build context and hands it to the devcontainer CLI:

```
~/.local/share/ai-agent/devcontainer/<id>/     ($AI_AGENT_DATA_DIR)
├── .devcontainer/          (Dockerfile, devcontainer.json, entrypoint.sh)
└── bin/ai-agent            ← a copy of the ai-agent binary you invoked
```

Because the definition travels with the binary, a release install can run `ai-agent up` from any directory with no checkout, and the `ai-agent` inside the container is always the one you ran — upgrade the host binary and the next `ai-agent up --build` picks it up. The `<id>` is derived from the `--workspace` path, so each workspace gets its own container instead of silently re-entering another workspace's.

## What's inside the image

Ubuntu 24.04, all dependencies pinned:

| Tool | Version | Details |
|------|---------|---------|
| **Go** | 1.25.0 | Toolchain for project work |
| **Node.js** | 22.11.0 | LTS |
| **Python 3** | System | Entry-point socket probe |
| **git** | System | System package |
| **make** | System | Common project task runner |
| **jq** | System | JSON inspection helper |
| **unzip** | System | Archive extraction helper |
| **gh** | 2.65.0 | Pinned .deb release, wrapped through `ai-agent-gh` |
| **claude** | 2.1.84 | `@anthropic-ai/claude-code` via npm |
| **codex** | 0.116.0 | `@openai/codex` via npm |
| **ai-agent** | Staged from the host | Multi-call binary: launcher, git credential shim, gh wrapper shim |

Runs as non-root user `dev` (UID 1000). The entrypoint validates broker socket wiring and fails fast rather than dropping you into a shell that will fail later. Optional agent CLIs such as Gemini require a custom image or a devcontainer extension.

`scripts/refresh-pins.sh` checks for newer upstream versions of every pin.

## Runtime hardening

| Setting | Effect |
|---------|--------|
| `--cap-drop=ALL` | No Linux capabilities granted |
| `--security-opt=no-new-privileges` | Prevents privilege escalation via setuid, etc. |
| `--read-only` | Immutable root filesystem |
| `--tmpfs=/tmp:rw,noexec,nosuid,size=512m` | Writable scratch, no executable code |
| `--userns=keep-id:uid=1000,gid=1000` | Maps the host UID onto the fixed `dev` user for rootless Podman |

Three writable mounts enter the container:

- **Workspace** (`$AI_AGENT_WORKSPACE` → `/workspace`) — your repos
- **Broker socket** (`$XDG_RUNTIME_DIR/ai-agent` → `/run/ai-agent`) — the socket only, no keys
- **Agent home** (`ai-agent-home` volume → `/home/dev`) — agent logins, CLI config, dotfiles

## Agent login state

`/home/dev` is the one supported personal state location, backed by the named `ai-agent-home` volume. Claude Code and Codex keep their sign-in and config there, so you sign in once; the volume is remounted on re-entry and survives container replacement.

`ai-agent auth status` runs each agent's native login probe (`claude auth status --json`, `codex login status`), reports whether login state is persisted, and gives remediation for a missing login. It never touches brokered GitHub credentials.

The integration suite proves login-state persistence across container replacement for both agents. Codex uses its real `login --with-api-key` and `login status` commands. Claude Code has no non-interactive online login, so the suite covers the two offline persisted-login paths it supports: an `apiKeyHelper` in `~/.claude/settings.json`, and a persisted OAuth credentials file at `~/.claude/.credentials.json`. After the container is replaced, both are still present and recognized as a login by `claude auth status` and `ai-agent auth status`. The tests assert persistence and local recognition only — not a provider-backed authenticated request — so a live browser OAuth sign-in and token refresh remain a manual first-time step.

Keep repo credentials out of this state. GitHub access is brokered: `git` uses `ai-agent-credential-helper`, `gh` uses `ai-agent-gh`. Do not run `gh auth login` in the container; the wrapper rejects credential-writing `gh auth` commands.

## Project-aware mode

The generic image carries Go, Node, and Python plus the agent CLIs. It does not provision a project's own stack (say Ruby + Postgres + Redis). Point `--project` at a repo that has its own `.devcontainer` and ai-agent runs **that** devcontainer — its features, `dockerComposeFile` services, `forwardPorts`, and `postCreateCommand` all apply — while injecting a broker overlay:

- the host broker socket is bind-mounted at `/run/ai-agent` and `AI_AGENT_AUTH_SOCK` is set;
- the host-installed `ai-agent` multi-call binary is bind-mounted read-only onto `PATH` under each of its invocation names (`ai-agent`, `ai-agent-gh`, `ai-agent-credential-helper`) and under every provider-declared interposed command name (currently `gh`), so a bare `gh` in the project shell resolves to the broker wrapper ahead of any project-provided binary;
- native Claude and Codex telemetry is routed through the host launcher;
- missing Codex and Claude guidance and the audit skill are installed in the container home.

```bash
ai-agent up --project ~/github/my-rails-app
```

The injected toolchain comes from the directory of the `ai-agent` binary you ran. If the project has no `.devcontainer`, ai-agent tells you to use `--workspace` for the generic image instead.

If the repo has a `.ai-agent/manifest.json` with schema `ai-agent-manifest/v2`, project mode also enforces the supported operating-model declarations before launching: `run_modes` must allow `project_devcontainer`, reserved ai-agent paths cannot be cache targets, declared caches are added as named volumes, declared ports are forwarded, declared Compose services are included in `runServices`, and a declared telemetry egress resource is projected as `AI_AGENT_OBSERVABILITY_RESOURCE`. Secret declarations remain broker resource bindings for managed runs; durable provider secret values are not copied into the devcontainer.

## Re-entering and stopping

The container keeps running after you exit the shell. `ai-agent up` prints the exact re-entry command, pointing at this workspace's own context (`<id>` is derived from the `--workspace` path):

```bash
devcontainer exec --workspace-folder ~/.local/share/ai-agent/devcontainer/<id> bash
```

To find or remove the backing container through the runtime directly (use the same `<id>` from the printed command):

```bash
FOLDER="$HOME/.local/share/ai-agent/devcontainer/<id>"
podman ps --filter "label=devcontainer.local_folder=$FOLDER"
CID=$(podman ps -q --filter "label=devcontainer.local_folder=$FOLDER")
podman stop "$CID" && podman rm "$CID"
```

Docker is the same with `docker ps` / `docker stop`.

## Driving the container by hand

Once an image exists (built by `ai-agent up`, or from a checkout — see [Building From Source](../design/build-from-source.md)), you can run it directly with the same confinement `ai-agent up` applies:

```bash
podman run -it --rm \
  --userns=keep-id:uid=1000,gid=1000 \
  --cap-drop=ALL \
  --security-opt=no-new-privileges \
  --read-only \
  --tmpfs=/tmp:rw,noexec,nosuid,size=512m \
  -v ai-agent-home:/home/dev \
  -v "$HOME/github:/workspace:Z" \
  -v "$XDG_RUNTIME_DIR/ai-agent:/run/ai-agent" \
  -e AI_AGENT_AUTH_SOCK=/run/ai-agent/broker.sock \
  --name ai-agent-dev \
  ai-agent-dev \
  bash
```

Swap `bash` for `sleep infinity` with `-d` to run detached, then `podman exec -it ai-agent-dev bash`.

| Flag | Why |
|------|-----|
| `--userns=keep-id` | Maps your host UID into the container so file ownership is correct |
| `--cap-drop=ALL` | Drops all Linux capabilities |
| `--read-only` | Prevents writes to the root filesystem |
| `-v .../ai-agent:/run/ai-agent` | Mounts the broker socket directory — no keys enter the container |
| `-e AI_AGENT_AUTH_SOCK=...` | Tells the shims where to find the broker |
| `-v ai-agent-home:/home/dev` | Persistent agent home |
| `:Z` on a volume | SELinux relabel for rootless Podman |

To drive the devcontainer CLI yourself: start the broker, export `XDG_RUNTIME_DIR` and `AI_AGENT_WORKSPACE`, then run `devcontainer up --workspace-folder <context>` and `devcontainer exec --workspace-folder <context> bash`. Opening a source checkout in VS Code with **"Dev Containers: Reopen in Container"** is covered in [Building From Source](../design/build-from-source.md).
