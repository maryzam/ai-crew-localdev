#!/usr/bin/env bash
set -euo pipefail

ROOT="$(git rev-parse --show-toplevel)"
gate="$ROOT/scripts/check-adr-gate.sh"

workdir="$(mktemp -d)"
trap 'rm -rf "$workdir"' EXIT

cd "$workdir"
git init -q
git config user.email "test@example.com"
git config user.name "test"

mkdir -p internal/configmodel/policy
printf 'package policy\n' >internal/configmodel/policy/validate.go
git add .
git commit -q -m "base"
base_sha="$(git rev-parse HEAD)"

printf 'package policy\n\nfunc Changed() {}\n' >internal/configmodel/policy/validate.go
git add .
git commit -q -m "touch policy"
head_sha="$(git rev-parse HEAD)"

status=0

empty_body="$workdir/empty-body.txt"
: >"$empty_body"
if "$gate" --range "$base_sha" "$head_sha" --body-file "$empty_body" >/tmp/out 2>&1; then
	printf 'FAIL: expected gate to require an ADR for internal/configmodel/policy change\n' >&2
	status=1
else
	printf 'PASS: gate requires an ADR for internal/configmodel/policy change\n'
fi

opt_out_body="$workdir/opt-out-body.txt"
printf '[no-adr]\n' >"$opt_out_body"
if "$gate" --range "$base_sha" "$head_sha" --body-file "$opt_out_body" >/tmp/out 2>&1; then
	printf 'PASS: [no-adr] opts out of the gate for internal/configmodel/policy change\n'
else
	printf 'FAIL: expected [no-adr] to opt out of the gate\n' >&2
	status=1
fi

git checkout -q "$base_sha" -- internal/configmodel/policy/validate.go
rm -rf internal
mkdir -p internal/broker/core
printf 'package core\n' >internal/broker/core/server.go
git add .
git commit -q -m "touch broker core"
broker_head_sha="$(git rev-parse HEAD)"

if "$gate" --range "$base_sha" "$broker_head_sha" --body-file "$empty_body" >/tmp/out 2>&1; then
	printf 'FAIL: expected gate to require an ADR for internal/broker/core change\n' >&2
	status=1
else
	printf 'PASS: gate requires an ADR for internal/broker/core change\n'
fi

exit "$status"
