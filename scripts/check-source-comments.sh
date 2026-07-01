#!/usr/bin/env bash
set -euo pipefail

ROOT="$(git rev-parse --show-toplevel)"
cd "$ROOT"

case "${1:-}" in
	--index)
		exec go run ./internal/quality/cmd/check-source-comments -index
		;;
	"")
		exec go run ./internal/quality/cmd/check-source-comments
		;;
	*)
		exec go run ./internal/quality/cmd/check-source-comments -ref "$1"
		;;
esac
