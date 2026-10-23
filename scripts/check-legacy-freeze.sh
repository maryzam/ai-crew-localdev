#!/usr/bin/env bash
set -euo pipefail

fail() {
	printf 'legacy freeze failed: %s\n' "$1" >&2
	exit 2
}

changed_between() {
	git diff --no-renames --name-only "$1" "$2" || fail "cannot compare $1 with $2; fetch both commits"
}

if [ "$#" -eq 2 ]; then
	changes=$(changed_between "$1" "$2")
elif [ "$#" -eq 0 ]; then
	base_ref=${LEGACY_FREEZE_BASE:-origin/main}
	base=$(git merge-base "$base_ref" HEAD) || fail "cannot find the merge base with $base_ref; fetch it or set LEGACY_FREEZE_BASE"
	committed=$(changed_between "$base" HEAD)
	uncommitted=$(git diff --no-renames --name-only HEAD) || fail "cannot list uncommitted changes"
	untracked=$(git ls-files --others --exclude-standard) || fail "cannot list untracked files"
	changes=$(printf '%s\n%s\n%s\n' "$committed" "$uncommitted" "$untracked" | sort -u)
else
	printf 'usage: %s [base-sha head-sha]\n' "$0" >&2
	exit 2
fi

failed=0
while IFS= read -r path; do
	case "$path" in
		internal/product/*)
			;;
		.devcontainer/*|cmd/ai-agent/*|internal/*)
			printf 'legacy freeze failed: %s is read-only; extract the reviewed mechanism into internal/product\n' "$path" >&2
			failed=1
			;;
	esac
done <<<"$changes"

exit "$failed"
