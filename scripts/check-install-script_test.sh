#!/usr/bin/env bash
set -euo pipefail

ROOT="$(git rev-parse --show-toplevel)"
installer="$ROOT/scripts/install.sh"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

arch="$(uname -m)"
case "$arch" in
x86_64) goarch=amd64 ;;
aarch64 | arm64) goarch=arm64 ;;
*)
	printf 'SKIP: unsupported test architecture %s\n' "$arch"
	exit 0
	;;
esac

release_dir="$tmp/releases/v0.0.0-test"
mkdir -p "$release_dir"
printf '#!/bin/sh\necho fake-ai-agent\n' >"$release_dir/ai-agent-linux-$goarch"
(cd "$release_dir" && sha256sum "ai-agent-linux-$goarch" >SHA256SUMS)

install_dir="$tmp/bin"
if ! AI_AGENT_RELEASE_BASE_URL="$tmp/releases" AI_AGENT_INSTALL_DIR="$install_dir" sh "$installer" v0.0.0-test >"$tmp/install.log" 2>&1; then
	printf 'FAIL: install from a valid local artifact failed:\n' >&2
	cat "$tmp/install.log" >&2
	exit 1
fi
if [ ! -x "$install_dir/ai-agent" ]; then
	printf 'FAIL: ai-agent binary missing or not executable after install\n' >&2
	exit 1
fi
for name in ai-agent-broker ai-agent-gh ai-agent-credential-helper; do
	if [ "$(readlink "$install_dir/$name")" != "ai-agent" ]; then
		printf 'FAIL: %s is not a symlink to ai-agent\n' "$name" >&2
		exit 1
	fi
done
printf 'PASS: valid artifact installs binary and invocation symlinks\n'

tampered_dir="$tmp/bin-tampered"
printf 'tampered-content\n' >>"$release_dir/ai-agent-linux-$goarch"
if AI_AGENT_RELEASE_BASE_URL="$tmp/releases" AI_AGENT_INSTALL_DIR="$tampered_dir" sh "$installer" v0.0.0-test >"$tmp/tampered.log" 2>&1; then
	printf 'FAIL: install accepted an artifact whose checksum does not match SHA256SUMS\n' >&2
	exit 1
fi
if ! grep -q 'checksum mismatch' "$tmp/tampered.log"; then
	printf 'FAIL: tampered install did not report a checksum mismatch:\n' >&2
	cat "$tmp/tampered.log" >&2
	exit 1
fi
if [ -e "$tampered_dir/ai-agent" ]; then
	printf 'FAIL: tampered artifact was installed despite checksum mismatch\n' >&2
	exit 1
fi
printf 'PASS: tampered artifact fails closed with nothing installed\n'

missing_dir="$tmp/bin-missing"
rm "$release_dir/SHA256SUMS"
if AI_AGENT_RELEASE_BASE_URL="$tmp/releases" AI_AGENT_INSTALL_DIR="$missing_dir" sh "$installer" v0.0.0-test >"$tmp/missing.log" 2>&1; then
	printf 'FAIL: install succeeded without SHA256SUMS\n' >&2
	exit 1
fi
if [ -e "$missing_dir/ai-agent" ]; then
	printf 'FAIL: artifact installed without checksum verification\n' >&2
	exit 1
fi
printf 'PASS: missing SHA256SUMS fails closed with nothing installed\n'
