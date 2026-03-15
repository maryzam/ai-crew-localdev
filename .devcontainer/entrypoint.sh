#!/usr/bin/env bash
set -euo pipefail

# Validate broker socket availability.
# Warn but do not block — ai-agent run will fail closed if the broker
# is unreachable, which is the correct enforcement point.
sock="${AI_AGENT_AUTH_SOCK:-/run/ai-agent/broker.sock}"
if [[ ! -S "$sock" ]]; then
    echo >&2 "ai-agent: broker socket not found at $sock"
    echo >&2 "ai-agent: ensure the host broker is running:"
    echo >&2 "  systemctl --user start ai-agent-broker"
fi

exec "$@"
