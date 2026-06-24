#!/usr/bin/env bash
# refresh-pins.sh — prints the current latest versions for all pinned
# dependencies in the devcontainer Dockerfile. Read-only helper; does not
# modify any files.
set -euo pipefail

echo "=== Pinned dependency versions (check against .devcontainer/Dockerfile) ==="
echo

echo "--- Go ---"
curl -fsSL "https://go.dev/dl/?mode=json" | jq -r '.[0].version'

echo
echo "--- Node.js 22 LTS ---"
curl -fsSL "https://nodejs.org/dist/index.json" | jq -r '[.[] | select(.lts != false) | select(.version | startswith("v22"))][0].version'

echo
echo "--- GitHub CLI ---"
curl -fsSL "https://api.github.com/repos/cli/cli/releases/latest" | jq -r '.tag_name'

echo
echo "--- NPM packages ---"
for pkg in @anthropic-ai/claude-code @openai/codex; do
  ver=$(npm view "$pkg" version 2>/dev/null || echo "not found")
  echo "  $pkg@$ver"
done

echo
echo "--- Container base images ---"
echo "  golang: $(docker pull --quiet golang:1.25.0 2>/dev/null && docker inspect --format='{{index .RepoDigests 0}}' golang:1.25.0 2>/dev/null || echo 'pull to get digest')"
echo "  ubuntu: $(docker pull --quiet ubuntu:24.04 2>/dev/null && docker inspect --format='{{index .RepoDigests 0}}' ubuntu:24.04 2>/dev/null || echo 'pull to get digest')"
echo "  node:   $(docker pull --quiet node:22.11.0-bookworm-slim 2>/dev/null && docker inspect --format='{{index .RepoDigests 0}}' node:22.11.0-bookworm-slim 2>/dev/null || echo 'pull to get digest')"
