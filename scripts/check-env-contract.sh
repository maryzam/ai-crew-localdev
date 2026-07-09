#!/usr/bin/env bash
set -euo pipefail

ROOT="$(git rev-parse --show-toplevel)"
cd "$ROOT"

violations=$(grep -rn 'Getenv("AI_AGENT_\|LookupEnv("AI_AGENT_' --include='*.go' internal/ cmd/ 2>/dev/null | grep -v '_test\.go' | grep -v '^internal/platform/paths/' || true)

if [ -n "$violations" ]; then
	printf 'environment contract failed: AI_AGENT_* variables must be read through named constants and resolvers in internal/platform/paths\n%s\n' "$violations" >&2
	exit 1
fi
