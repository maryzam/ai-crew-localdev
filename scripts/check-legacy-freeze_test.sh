#!/usr/bin/env bash
set -euo pipefail

root=$(mktemp -d)
trap 'rm -rf "$root"' EXIT
cp scripts/check-legacy-freeze.sh "$root/check.sh"
cd "$root"
git init -q
git config user.name test
git config user.email test@example.invalid
mkdir -p internal/runtime/launcher internal/modelproxy internal/product/login
printf 'old\n' >internal/runtime/launcher/legacy.go
printf 'old\n' >internal/modelproxy/proxy.go
printf 'new\n' >internal/product/login/login.go
git add .
git commit -qm base
base=$(git rev-parse HEAD)

expect_pass() {
	if ! "$@" 2>/dev/null; then
		printf 'legacy freeze rejected an allowed change: %s\n' "$label" >&2
		exit 1
	fi
}

expect_fail() {
	if "$@" 2>/dev/null; then
		printf 'legacy freeze accepted a forbidden change: %s\n' "$label" >&2
		exit 1
	fi
}

commit() {
	git add -A
	git commit -qm "$1"
	git rev-parse HEAD
}

label="greenfield edit"
printf 'changed\n' >internal/product/login/login.go
greenfield=$(commit greenfield)
expect_pass ./check.sh "$base" "$greenfield"
LEGACY_FREEZE_BASE=$base expect_pass ./check.sh

label="post-pivot package edit"
printf 'changed\n' >internal/modelproxy/proxy.go
edited=$(commit modelproxy)
expect_fail ./check.sh "$greenfield" "$edited"

label="move out of the legacy tree"
git reset -q --hard "$greenfield"
git mv internal/runtime/launcher/legacy.go internal/product/login/legacy.go
moved=$(commit move)
expect_fail ./check.sh "$greenfield" "$moved"

label="committed legacy edit in local mode"
LEGACY_FREEZE_BASE=$greenfield expect_fail ./check.sh

label="untracked legacy file in local mode"
git reset -q --hard "$greenfield"
printf 'new\n' >internal/runtime/launcher/added.go
LEGACY_FREEZE_BASE=$greenfield expect_fail ./check.sh
rm internal/runtime/launcher/added.go

label="unknown commit"
expect_fail ./check.sh "$base" 0123456789abcdef0123456789abcdef01234567

label="missing merge base"
LEGACY_FREEZE_BASE=refs/heads/absent expect_fail ./check.sh
