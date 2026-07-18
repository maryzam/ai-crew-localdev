# Building From Source

**Scope (builder track): how to build, install, and verify the tool from a checkout, the binary layout, and the embedded-asset contract.** Users installing a release do not need any of this — see [Setup](../guide/setup.md).

## Build and install

```bash
git clone https://github.com/maryzam/ai-crew-localdev.git
cd ai-crew-localdev
make build      # builds the four binary roles (static, CGO disabled)
make install    # copies the multi-call binary and invocation symlinks to $GOBIN, or ~/.local/bin when GOBIN is unset
make test       # go test ./...
make lint       # golangci-lint run
```

Requires Go 1.25+. Confirm the install directory is on `PATH` with `which ai-agent`.

## The multi-call binary

One binary, `ai-agent`, selects its role by invocation name through symlinks:

| Invocation name | Role |
|-----------------|------|
| `ai-agent` | Main CLI |
| `ai-agent-broker` | Host daemon that holds keys and issues credentials |
| `ai-agent-credential-helper` | Git credential helper shim |
| `ai-agent-gh` (also `gh`) | gh CLI wrapper shim |

Keeping the deployable surface to one binary is a core invariant — see [Design Principles](design-principles.md) and [Architecture](architecture.md).

## The build-context contract

The generic devcontainer definition ships **inside the `ai-agent` binary**. On `ai-agent up`, the binary stages a build context and hands it to the devcontainer CLI:

```
~/.local/share/ai-agent/devcontainer/<id>/     ($AI_AGENT_DATA_DIR, <id> per workspace)
├── .devcontainer/
│   ├── Dockerfile
│   ├── devcontainer.json
│   └── entrypoint.sh
└── bin/
    └── ai-agent          ← a copy of the ai-agent binary you invoked
```

The image installs that staged binary rather than compiling from a source tree. Two consequences:

- **No checkout is required at runtime.** A release install can run `ai-agent up` from any directory. The image must therefore never reintroduce a `go build` step — an invariant test enforces this.
- **The `ai-agent` inside the container is the one you ran.** Upgrade the host binary and the next `ai-agent up --build` picks it up; the context is restaged on every run.

The canonical asset sources are `.devcontainer/` and `contrib/langfuse/` in the repository. They are mirrored into the binary's embedded copies by `make devcontainer-assets` and `make langfuse-assets`. A drift test fails if a canonical source and its embedded copy diverge, so run those targets after editing either tree.

## Building the image by hand

`make build` stages `bin/ai-agent` into the build context, so a direct image build works from the checkout:

```bash
make build
podman build -f .devcontainer/Dockerfile -t ai-agent-dev .
```

In VS Code, open the checkout and use **Ctrl+Shift+P → "Dev Containers: Reopen in Container"** — run `make build` first so the image has a binary to install.

## Verification gates

`make verify` (and `ai-agent check` for noisy commands) runs the same gates CI enforces: tests, `go vet`, lint, the source-comment check, the asset-drift invariants, and the telemetry-schema drift check. The ADR gate (`scripts/check-adr-gate.sh`) requires an ADR under `docs/decisions/` when a change touches the broker or policy model. The end-to-end readiness and clean-host journey targets are described in the repository [README](../../README.md).
