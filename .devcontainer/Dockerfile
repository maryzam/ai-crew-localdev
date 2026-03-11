# Build stage: compile ai-agent binaries
FROM golang:1 AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN make build

# Runtime stage
FROM ubuntu:24.04

ARG DEBIAN_FRONTEND=noninteractive

# System packages
RUN apt-get update && apt-get install -y --no-install-recommends \
    git \
    curl \
    ca-certificates \
    gnupg \
    make \
    python3 \
    python3-pip \
    python3-venv \
    && rm -rf /var/lib/apt/lists/*

# gh CLI (official GitHub repository)
RUN curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg \
      | gpg --dearmor -o /usr/share/keyrings/githubcli-archive-keyring.gpg \
    && echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/githubcli-archive-keyring.gpg] https://cli.github.com/packages stable main" \
      > /etc/apt/sources.list.d/github-cli.list \
    && apt-get update && apt-get install -y --no-install-recommends gh \
    && rm -rf /var/lib/apt/lists/*

# Go (latest stable, copied from build stage)
COPY --from=builder /usr/local/go /usr/local/go
ENV PATH="/usr/local/go/bin:${PATH}"

# Node.js LTS (copied from official image)
COPY --from=node:lts-slim /usr/local/bin/node /usr/local/bin/node
COPY --from=node:lts-slim /usr/local/lib/node_modules /usr/local/lib/node_modules
RUN ln -sf ../lib/node_modules/npm/bin/npm-cli.js /usr/local/bin/npm \
    && ln -sf ../lib/node_modules/npm/bin/npx-cli.js /usr/local/bin/npx

# Non-root user for rootless Podman compatibility.
# Ubuntu 24.04 ships with a "ubuntu" user at UID 1000; remove it first.
# updateRemoteUserUID in devcontainer.json adjusts the UID to match the host.
RUN userdel -r ubuntu 2>/dev/null || true \
    && useradd -m -s /bin/bash -u 1000 dev \
    && mkdir -p /home/dev/go /home/dev/.gemini \
    && chown -R dev:dev /home/dev

ENV GOPATH="/home/dev/go"
ENV PATH="/home/dev/go/bin:${PATH}"

# AI agent binaries (from build stage)
COPY --from=builder /src/bin/ai-agent /usr/local/bin/
COPY --from=builder /src/bin/ai-agent-credential-helper /usr/local/bin/
COPY --from=builder /src/bin/ai-agent-gh /usr/local/bin/

# gh wrapper: routes all gh invocations through ai-agent-gh for brokered auth.
# Outside a managed session (no AI_AGENT_AUTH_SOCK), gh fails closed.
# For unmanaged access, use /usr/bin/gh directly.
RUN printf '#!/bin/sh\nexec /usr/local/bin/ai-agent-gh "$@"\n' > /usr/local/bin/gh \
    && chmod +x /usr/local/bin/gh

# AI agent CLIs (installed as root so they land in /usr/local/bin)
RUN npm install -g @anthropic-ai/claude-code @openai/codex @google/gemini-cli

# Entrypoint
COPY .devcontainer/entrypoint.sh /usr/local/bin/ai-agent-entrypoint
RUN chmod +x /usr/local/bin/ai-agent-entrypoint

USER dev
WORKDIR /workspace
ENTRYPOINT ["/usr/local/bin/ai-agent-entrypoint"]
CMD ["bash"]
