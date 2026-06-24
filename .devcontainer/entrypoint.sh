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

require_env() {
    local name="$1"
    local hint="$2"
    if [[ -z "${!name:-}" ]]; then
        fail "$name is not set. $hint"
    fi
}

require_env "AI_AGENT_AUTH_SOCK" "The devcontainer must mount the host broker socket at /run/ai-agent/broker.sock."

sock="${AI_AGENT_AUTH_SOCK}"
workspace_dir="${AI_AGENT_WORKSPACE_DIR:-/workspace}"
home_dir="${HOME:-/home/dev}"
current_uid="$(id -u)"

if [[ ! -d "$workspace_dir" ]]; then
    fail "workspace mount is not ready at $workspace_dir; check AI_AGENT_WORKSPACE on the host and the workspaceMount setting"
fi

if [[ ! -w "$workspace_dir" ]]; then
    fail "workspace directory $workspace_dir is not writable by uid $current_uid; verify --userns=keep-id:uid=1000,gid=1000 and the workspace bind mount"
fi

# The home volume must be writable by the mapped UID or agent logins silently
# fail to persist.
if [[ ! -w "$home_dir" ]]; then
    fail "home directory $home_dir is not writable by uid $current_uid; the ai-agent-home volume ownership does not match --userns=keep-id:uid=1000,gid=1000"
fi

if [[ ! -e "$sock" ]]; then
    fail "broker socket not found at $sock; start the host broker with 'systemctl --user start ai-agent-broker.socket' and relaunch the devcontainer"
fi

sock_type="$(stat -Lc '%F' "$sock" 2>/dev/null || echo 'unexpected file type')"
if [[ "$sock_type" != "socket" ]]; then
    fail "expected a Unix socket at $sock, found $sock_type; fix the devcontainer mount so it points at the host broker socket"
fi

owner_uid="$(stat -Lc '%u' "$sock" 2>/dev/null || echo '?')"
owner_gid="$(stat -Lc '%g' "$sock" 2>/dev/null || echo '?')"
mode="$(stat -Lc '%a' "$sock" 2>/dev/null || echo '???')"
mode_octal=$((8#${mode:-0}))

if [[ "$owner_uid" != "$current_uid" ]]; then
    fail "broker socket $sock is owned by uid $owner_uid (gid $owner_gid), expected uid $current_uid; fix the socket ownership or rootless user mapping on the host"
fi

if (( (mode_octal & 077) != 0 )); then
    fail "broker socket $sock has mode $mode; expected owner-only permissions such as 600"
fi

if [[ ! -r "$sock" || ! -w "$sock" ]]; then
    fail "broker socket $sock is not accessible to uid $current_uid; verify bind-mount permissions and SELinux relabeling if applicable"
fi

if ! python3 - "$sock" <<'PY'
import socket
import sys

path = sys.argv[1]
client = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
try:
    client.settimeout(1.5)
    client.connect(path)
finally:
    client.close()
PY
then
    fail "broker socket $sock is present but not accepting connections; start or restart the host broker and relaunch the devcontainer"
fi

exec "$@"
