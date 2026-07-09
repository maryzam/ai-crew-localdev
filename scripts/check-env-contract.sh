#!/usr/bin/env bash
set -euo pipefail

ROOT="$(git rev-parse --show-toplevel)"
cd "$ROOT"

violations=$(grep -rnE 'AI_AGENT_[A-Z]' --include='*.go' internal/ cmd/ 2>/dev/null | grep -v '_test\.go' | grep -v '^internal/platform/paths/' || true)

if [ -n "$violations" ]; then
	printf 'environment contract failed: every AI_AGENT_* name must be a named constant in internal/platform/paths; compose from the constants instead of string literals\n%s\n' "$violations" >&2
	exit 1
fi
