# Build stage: compile ai-agent binaries
FROM golang:1.25.0 AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN make build

# Runtime stage
# ubuntu:24.04 — pin tag; run scripts/refresh-pins.sh to check digest
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
    unzip \
    jq \
    && rm -rf /var/lib/apt/lists/*

# gh CLI — pinned .deb release for reproducibility.
# The real binary is moved off PATH into a private location so the only "gh"
# an agent can invoke is the brokered wrapper installed below. This closes the
# bypass where an agent calls the unmanaged gh directly with personal auth.
ARG GH_VERSION=2.65.0
ARG REAL_GH=/opt/ai-agent/bin/gh
RUN curl -fsSL "https://github.com/cli/cli/releases/download/v${GH_VERSION}/gh_${GH_VERSION}_linux_$(dpkg --print-architecture).deb" \
      -o /tmp/gh.deb \
    && dpkg -i /tmp/gh.deb \
    && rm /tmp/gh.deb \
    && mkdir -p "$(dirname "${REAL_GH}")" \
    && mv /usr/bin/gh "${REAL_GH}"
ENV AI_AGENT_REAL_GH=/opt/ai-agent/bin/gh

# Go (latest stable, copied from build stage)
COPY --from=builder /usr/local/go /usr/local/go
ENV PATH="/usr/local/go/bin:${PATH}"

# Node.js 22 LTS — pinned version for reproducibility
COPY --from=node:22.11.0-bookworm-slim /usr/local/bin/node /usr/local/bin/node
COPY --from=node:22.11.0-bookworm-slim /usr/local/lib/node_modules /usr/local/lib/node_modules
RUN ln -sf ../lib/node_modules/npm/bin/npm-cli.js /usr/local/bin/npm \
    && ln -sf ../lib/node_modules/npm/bin/npx-cli.js /usr/local/bin/npx

# Non-root user for rootless Podman compatibility.
# Ubuntu 24.04 ships with a "ubuntu" user at UID 1000; remove it first.
# With --userns=keep-id, Podman maps host UID into the container;
# updateRemoteUserUID=false avoids chown conflicts under rootless Podman.
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
RUN printf '#!/bin/sh\nexec /usr/local/bin/ai-agent-gh "$@"\n' > /usr/local/bin/gh \
    && chmod +x /usr/local/bin/gh

# AI agent CLIs — pinned versions for reproducibility
RUN npm install -g \
    @anthropic-ai/claude-code@2.1.84 \
    @openai/codex@0.116.0 \
    @google/gemini-cli@0.35.1

# Entrypoint
COPY .devcontainer/entrypoint.sh /usr/local/bin/ai-agent-entrypoint
RUN chmod +x /usr/local/bin/ai-agent-entrypoint

USER dev
WORKDIR /workspace
ENTRYPOINT ["/usr/local/bin/ai-agent-entrypoint"]
CMD ["bash"]
