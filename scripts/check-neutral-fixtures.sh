#!/usr/bin/env bash
set -euo pipefail

ROOT="$(git rev-parse --show-toplevel)"
cd "$ROOT"

status=0

fail_matches() {
  local label="$1"
  local matches="$2"
  if [ -n "$matches" ]; then
    printf 'neutral fixture check failed: %s\n%s\n' "$label" "$matches" >&2
    status=1
  fi
}

is_neutral_owner() {
  [[ "$1" =~ ^(example-[a-z0-9][a-z0-9-]*|owner|org|o|new|journey)$ ]]
}

is_neutral_repo() {
  [[ "$1" =~ ^(example-[a-z0-9][a-z0-9-]*|repo|repo-[a-z0-9][a-z0-9-]*|name|one|two|r[0-9]*|other-repo|not-allowed|different)$ ]]
}

is_neutral_home() {
  [[ "$1" =~ ^(dev|test|user|you|me|source|example|example-agent)$ ]]
}

fixture_globs=(
  --glob '*_test.go'
  --glob 'testdata/**'
  --glob 'docs/decisions/**'
)

task_ref_pattern='github:([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)'$'\043''[0-9]+'
task_ref_search='github:[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+'$'\043''[0-9]+'

github_matches=$(
  while IFS= read -r line; do
    if [[ "$line" =~ github:repo:([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+) ]]; then
      owner="${BASH_REMATCH[1]}"
      repo="${BASH_REMATCH[2]}"
      if ! is_neutral_owner "$owner" || ! is_neutral_repo "$repo"; then
        printf '%s\n' "$line"
      fi
    fi
  done < <(rg -n --no-heading 'github:repo:[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+' "${fixture_globs[@]}" || true)
)
fail_matches "non-neutral GitHub repository resource in fixture or example data" "$github_matches"

task_matches=$(
  while IFS= read -r line; do
    if [[ "$line" =~ $task_ref_pattern ]]; then
      owner="${BASH_REMATCH[1]}"
      repo="${BASH_REMATCH[2]}"
      if ! is_neutral_owner "$owner" || ! is_neutral_repo "$repo"; then
        printf '%s\n' "$line"
      fi
    fi
  done < <(rg -n --no-heading "$task_ref_search" "${fixture_globs[@]}" || true)
)
fail_matches "non-neutral GitHub task reference in fixture or example data" "$task_matches"

url_matches=$(
  while IFS= read -r line; do
    if [[ "$line" =~ https://github\.com/([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)(\.git)? ]]; then
      owner="${BASH_REMATCH[1]}"
      repo="${BASH_REMATCH[2]}"
      repo="${repo%.git.extraheader}"
      repo="${repo%.extraheader}"
      repo="${repo%.git}"
      if ! is_neutral_owner "$owner" || ! is_neutral_repo "$repo"; then
        printf '%s\n' "$line"
      fi
    fi
  done < <(rg -n --no-heading 'https://github\.com/[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+(\.git)?' "${fixture_globs[@]}" || true)
)
fail_matches "non-neutral GitHub URL in fixture or example data" "$url_matches"

home_matches=$(
  while IFS= read -r line; do
    if [[ "$line" =~ /home/([A-Za-z0-9_.-]+) ]]; then
      name="${BASH_REMATCH[1]}"
      if ! is_neutral_home "$name"; then
        printf '%s\n' "$line"
      fi
    fi
  done < <(rg -n --no-heading '/home/[A-Za-z0-9_.-]+' "${fixture_globs[@]}" || true)
)
fail_matches "non-neutral home path in fixture or example data" "$home_matches"

exit "$status"
