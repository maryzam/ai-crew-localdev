#!/usr/bin/env bash
set -euo pipefail

# Validate broker socket availability for the container shell.
# The devcontainer itself is just the execution environment; managed sessions
# still begin later when the user runs `ai-agent run` inside the container.
sock="${AI_AGENT_AUTH_SOCK:-/run/ai-agent/broker.sock}"
if [[ ! -S "$sock" ]]; then
    echo >&2 "ai-agent: broker socket not found at $sock"
    echo >&2 "ai-agent: ensure the host broker is running:"
    echo >&2 "  systemctl --user start ai-agent-broker"
fi

exec "$@"
