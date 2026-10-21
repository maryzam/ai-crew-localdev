#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -eq 2 ]; then
	mapfile -t changed < <(git diff --name-only --diff-filter=ACMRTD "$1" "$2")
elif [ "$#" -eq 0 ]; then
	mapfile -t changed < <({ git diff --name-only --diff-filter=ACMRTD; git diff --cached --name-only --diff-filter=ACMRTD; } | sort -u)
else
	printf 'usage: %s [base-sha head-sha]\n' "$0" >&2
	exit 2
fi

failed=0
for path in "${changed[@]}"; do
	case "$path" in
		internal/product/*)
			;;
		.devcontainer/*|cmd/ai-agent/*|internal/*)
			printf 'legacy freeze failed: %s is read-only; copy the required mechanism into a greenfield package\n' "$path" >&2
			failed=1
			;;
	esac
done

exit "$failed"
