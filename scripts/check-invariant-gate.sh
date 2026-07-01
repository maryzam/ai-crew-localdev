#!/usr/bin/env bash
set -euo pipefail

ROOT="$(git rev-parse --show-toplevel)"
cd "$ROOT"

BASE_REF="${1:-${BASE_REF:-origin/main}}"
HEAD_REF="${2:-${HEAD_REF:-HEAD}}"
MERGE_BASE="$(git merge-base "$BASE_REF" "$HEAD_REF")"
PR_TEXT="${PR_BODY:-}"
if [[ -n "${PR_BODY_FILE:-}" && -f "${PR_BODY_FILE:-}" ]]; then
  PR_TEXT="$(cat "$PR_BODY_FILE")"
fi

if [[ "$PR_TEXT" == *"[no-invariants]"* ]]; then
  printf 'Invariant gate skipped because PR body contains [no-invariants].\n'
  exit 0
fi

mapfile -t changed < <(
  git diff --name-only --diff-filter=ACMRTD "$MERGE_BASE" "$HEAD_REF" -- internal/broker internal/configmodel/policy
)

declare -A touched_dirs=()
declare -A test_dirs=()

for path in "${changed[@]}"; do
  [[ -n "$path" ]] || continue
  dir="$(dirname "$path")"
  if [[ "$path" == *_test.go ]]; then
    test_dirs["$dir"]=1
    continue
  fi
  touched_dirs["$dir"]=1
done

status=0
for dir in "${!touched_dirs[@]}"; do
  if [[ -n "${test_dirs[$dir]:-}" ]]; then
    continue
  fi
  printf '%s: high-risk change needs a same-package *_test.go or *_invariants_test.go change, or [no-invariants] in the PR body\n' "$dir" >&2
  status=1
done

exit "$status"
