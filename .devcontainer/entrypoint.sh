#!/usr/bin/env bash
set -euo pipefail

fail() {
    echo >&2 "ai-agent: devcontainer startup check failed: $*"
    exit 1
}

describe_path_type() {
    local path="$1"

    if [[ -S "$path" ]]; then
        echo "Unix socket"
    elif [[ -f "$path" ]]; then
        echo "regular file"
    elif [[ -d "$path" ]]; then
        echo "directory"
    else
        echo "unexpected file type"
    fi
}

sock="${AI_AGENT_AUTH_SOCK:-}"
if [[ -z "$sock" ]]; then
    fail "AI_AGENT_AUTH_SOCK is not set; the devcontainer must mount the host broker socket at /run/ai-agent/broker.sock"
fi

if [[ ! -e "$sock" ]]; then
    fail "broker socket not found at $sock; start the host broker with 'systemctl --user start ai-agent-broker.socket' and relaunch the devcontainer"
fi

if [[ ! -S "$sock" ]]; then
    fail "expected a Unix socket at $sock, found $(describe_path_type "$sock"); fix the devcontainer mount so it points at the host broker socket"
fi

if [[ ! -w "$sock" ]]; then
    owner_uid="$(stat -c '%u' "$sock" 2>/dev/null || echo '?')"
    owner_gid="$(stat -c '%g' "$sock" 2>/dev/null || echo '?')"
    mode="$(stat -c '%a' "$sock" 2>/dev/null || echo '???')"
    current_uid="$(id -u)"
    fail "broker socket at $sock is not writable by uid $current_uid (owner uid $owner_uid, gid $owner_gid, mode $mode); fix the socket ownership or rootless user mapping on the host"
fi

exec "$@"
