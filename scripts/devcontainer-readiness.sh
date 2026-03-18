#!/usr/bin/env bash
set -euo pipefail

root_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root_dir"

exec go test -tags=integration -timeout=30m ./internal/e2e -run TestDevcontainerReadiness -count=1
