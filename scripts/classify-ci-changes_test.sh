#!/usr/bin/env bash
set -euo pipefail

ROOT="$(git rev-parse --show-toplevel)"
classifier="$ROOT/scripts/classify-ci-changes.sh"
workdir="$(mktemp -d)"
trap 'rm -rf "$workdir"' EXIT

cd "$workdir"
git init -q
git config user.email "test@example.com"
git config user.name "test"

mkdir -p docs internal/app
printf 'base\n' >README.md
git add .
git commit -q -m base
base_sha="$(git rev-parse HEAD)"

status=0

run_case() {
	local name="$1"
	local base="$2"
	local head="$3"
	local want_docs="$4"
	local want_full="$5"
	local want_readiness="$6"
	local output="$workdir/$name.output"
	local log="$workdir/$name.log"

	if ! BASE_SHA="$base" HEAD_SHA="$head" EVENT_NAME=pull_request GITHUB_OUTPUT="$output" "$classifier" >"$log" 2>&1; then
		printf 'FAIL: %s classifier exited non-zero\n' "$name" >&2
		cat "$log" >&2
		status=1
		return
	fi

	local got_docs got_full got_readiness
	got_docs="$(grep '^docs_changed=' "$output" | cut -d= -f2)"
	got_full="$(grep '^full_verify=' "$output" | cut -d= -f2)"
	got_readiness="$(grep '^readiness=' "$output" | cut -d= -f2)"
	if [ "$got_docs" != "$want_docs" ] || [ "$got_full" != "$want_full" ] || [ "$got_readiness" != "$want_readiness" ]; then
		printf 'FAIL: %s got docs_changed=%s full_verify=%s readiness=%s\n' "$name" "$got_docs" "$got_full" "$got_readiness" >&2
		printf 'FAIL: %s want docs_changed=%s full_verify=%s readiness=%s\n' "$name" "$want_docs" "$want_full" "$want_readiness" >&2
		cat "$log" >&2
		status=1
		return
	fi
	printf 'PASS: %s\n' "$name"
}

git checkout -q -b docs-only "$base_sha"
mkdir -p docs
printf 'docs\n' >docs/guide.md
git add .
git commit -q -m docs-only
docs_only_sha="$(git rev-parse HEAD)"

git checkout -q -b docs-code "$base_sha"
mkdir -p docs internal/app
printf 'docs\n' >docs/guide.md
printf 'package app\n' >internal/app/app.go
git add .
git commit -q -m docs-code
docs_code_sha="$(git rev-parse HEAD)"

run_case docs-only "$base_sha" "$docs_only_sha" true false false
run_case docs-code "$base_sha" "$docs_code_sha" true true true
run_case empty-diff "$base_sha" "$base_sha" false true true
run_case fail-closed deadbeefdeadbeefdeadbeefdeadbeefdeadbeef "$docs_only_sha" true true true

exit "$status"
