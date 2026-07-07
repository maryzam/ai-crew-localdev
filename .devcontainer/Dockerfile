FROM golang:1.25.0 AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN make build

FROM ubuntu:24.04

ARG DEBIAN_FRONTEND=noninteractive

RUN apt-get update && apt-get install -y --no-install-recommends \
    git \
    curl \
    ca-certificates \
    make \
    python3 \
    ripgrep \
    unzip \
    jq \
    && rm -rf /var/lib/apt/lists/*

ARG GH_VERSION=2.65.0
ARG REAL_GH=/opt/ai-agent/bin/gh
RUN curl -fsSL "https://github.com/cli/cli/releases/download/v${GH_VERSION}/gh_${GH_VERSION}_linux_$(dpkg --print-architecture).deb" \
      -o /tmp/gh.deb \
    && dpkg -i /tmp/gh.deb \
    && rm /tmp/gh.deb \
    && mkdir -p "$(dirname "${REAL_GH}")" \
    && mv /usr/bin/gh "${REAL_GH}"
ENV AI_AGENT_REAL_GH=/opt/ai-agent/bin/gh

COPY --from=builder /usr/local/go /usr/local/go
ENV PATH="/usr/local/go/bin:${PATH}"

COPY --from=node:22.11.0-bookworm-slim /usr/local/bin/node /usr/local/bin/node
COPY --from=node:22.11.0-bookworm-slim /usr/local/lib/node_modules /usr/local/lib/node_modules
RUN ln -sf ../lib/node_modules/npm/bin/npm-cli.js /usr/local/bin/npm \
    && ln -sf ../lib/node_modules/npm/bin/npx-cli.js /usr/local/bin/npx

RUN userdel -r ubuntu 2>/dev/null || true \
    && useradd -m -s /bin/bash -u 1000 dev \
    && mkdir -p /home/dev/go \
    && chown -R dev:dev /home/dev

ENV GOPATH="/home/dev/go"
ENV PATH="/home/dev/go/bin:${PATH}"

COPY --from=builder /src/bin/ai-agent /usr/local/bin/

RUN ln -sf ai-agent /usr/local/bin/ai-agent-gh \
    && ln -sf ai-agent /usr/local/bin/ai-agent-credential-helper \
    && ln -sf ai-agent /usr/local/bin/gh

RUN npm install -g \
    @anthropic-ai/claude-code@2.1.84 \
    @openai/codex@0.116.0

COPY .devcontainer/entrypoint.sh /usr/local/bin/ai-agent-entrypoint
RUN chmod +x /usr/local/bin/ai-agent-entrypoint

USER dev
WORKDIR /workspace
ENTRYPOINT ["/usr/local/bin/ai-agent-entrypoint"]
CMD ["bash"]
