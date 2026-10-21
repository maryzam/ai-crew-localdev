#!/usr/bin/env bash
set -euo pipefail

root=$(mktemp -d)
trap 'rm -rf "$root"' EXIT
cp scripts/check-legacy-freeze.sh "$root/check.sh"
cd "$root"
git init -q
git config user.name test
git config user.email test@example.invalid
mkdir -p internal/runtime/launcher internal/product/login
printf 'old\n' >internal/runtime/launcher/legacy.go
printf 'new\n' >internal/product/login/login.go
git add .
git commit -qm base
base=$(git rev-parse HEAD)
printf 'changed\n' >internal/product/login/login.go
git add .
git commit -qm greenfield
head=$(git rev-parse HEAD)
./check.sh "$base" "$head"
printf 'changed\n' >internal/runtime/launcher/legacy.go
git add .
git commit -qm legacy
legacy=$(git rev-parse HEAD)
if ./check.sh "$head" "$legacy"; then
	printf 'legacy freeze accepted a legacy change\n' >&2
	exit 1
fi
