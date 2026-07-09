#!/usr/bin/env sh
set -eu

usage() {
	printf 'usage: install.sh <version-tag>\n' >&2
	printf 'environment: AI_AGENT_INSTALL_DIR (default ~/.local/bin), AI_AGENT_RELEASE_BASE_URL (default GitHub releases)\n' >&2
	exit 2
}

[ "$#" -eq 1 ] || usage
version=$1

base_url=${AI_AGENT_RELEASE_BASE_URL:-https://github.com/maryzam/ai-crew-localdev/releases/download}
install_dir=${AI_AGENT_INSTALL_DIR:-$HOME/.local/bin}

case "$(uname -s)" in
Linux) ;;
*)
	printf 'install.sh: only Linux is supported\n' >&2
	exit 1
	;;
esac

case "$(uname -m)" in
x86_64) arch=amd64 ;;
aarch64 | arm64) arch=arm64 ;;
*)
	printf 'install.sh: unsupported architecture %s\n' "$(uname -m)" >&2
	exit 1
	;;
esac

artifact="ai-agent-linux-${arch}"
workdir=$(mktemp -d)
trap 'rm -rf "$workdir"' EXIT

fetch() {
	source_path="${base_url}/${version}/$1"
	case "$base_url" in
	http://* | https://*)
		curl -fsSL -o "$workdir/$1" "$source_path"
		;;
	*)
		cp "$source_path" "$workdir/$1"
		;;
	esac
}

fetch "$artifact"
fetch SHA256SUMS

expected=$(grep " ${artifact}\$" "$workdir/SHA256SUMS" | awk '{print $1}')
if [ -z "$expected" ]; then
	printf 'install.sh: SHA256SUMS has no entry for %s\n' "$artifact" >&2
	exit 1
fi
actual=$(sha256sum "$workdir/$artifact" | awk '{print $1}')
if [ "$expected" != "$actual" ]; then
	printf 'install.sh: checksum mismatch for %s: expected %s, got %s; refusing to install\n' "$artifact" "$expected" "$actual" >&2
	exit 1
fi

mkdir -p "$install_dir"
install -m 0755 "$workdir/$artifact" "$install_dir/ai-agent"
for name in ai-agent-broker ai-agent-gh ai-agent-credential-helper; do
	ln -sf ai-agent "$install_dir/$name"
done

printf 'installed ai-agent %s and invocation symlinks to %s\n' "$version" "$install_dir"
printf 'verify: %s/ai-agent --version\n' "$install_dir"
