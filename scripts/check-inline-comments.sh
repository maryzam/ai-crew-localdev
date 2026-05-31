#!/usr/bin/env bash
set -euo pipefail

ROOT="$(git rev-parse --show-toplevel)"
cd "$ROOT"

BASE_REF="${1:-${BASE_REF:-origin/main}}"
HEAD_REF="${2:-${HEAD_REF:-HEAD}}"
MERGE_BASE="$(git merge-base "$BASE_REF" "$HEAD_REF")"

mapfile -t go_files < <(
  git diff --name-only --diff-filter=ACMR "$MERGE_BASE" "$HEAD_REF" -- '*.go'
)

if (( ${#go_files[@]} == 0 )); then
  exit 0
fi

tmpdir="$(mktemp -d)"
trap 'rm -rf "$tmpdir"' EXIT

added_lines="$tmpdir/added-lines.txt"
git diff --unified=0 --diff-filter=ACMR "$MERGE_BASE" "$HEAD_REF" -- '*.go' |
	awk '
		/^\+\+\+ b\// {
			file = substr($0, 7)
			next
		}
		/^@@ / {
			if (file == "" || match($0, /\+[0-9]+(,[0-9]+)?/) == 0) {
				next
			}
			range = substr($0, RSTART + 1, RLENGTH - 1)
			split(range, parts, ",")
			start = parts[1] + 0
			count = parts[2] == "" ? 1 : parts[2] + 0
			for (i = 0; i < count; i++) {
				print file ":" start + i
			}
		}
	' >"$added_lines"

go run ./internal/quality/cmd/check-inline-comments -added-lines "$added_lines" "${go_files[@]}"
