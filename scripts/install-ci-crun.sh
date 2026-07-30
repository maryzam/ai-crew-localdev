#!/usr/bin/env bash
set -euo pipefail

readonly CRUN_VERSION="1.28"
readonly CRUN_SHA256="2aa6b7024a9c9f153895c0d11ae233d3758f54844011c3a039e3e89048d01d42"
readonly CRUN_URL="https://github.com/containers/crun/releases/download/${CRUN_VERSION}/crun-${CRUN_VERSION}-linux-amd64"

tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT

curl -fsSL --retry 3 --proto '=https' --tlsv1.2 -o "$tmp" "$CRUN_URL"
printf '%s  %s\n' "$CRUN_SHA256" "$tmp" | sha256sum -c -
sudo install -m 0755 "$tmp" /usr/bin/crun

installed="$(crun --version | sed -n '1p')"
case "$installed" in
*"version ${CRUN_VERSION}"*) ;;
*)
	echo "unexpected crun version: ${installed}" >&2
	exit 1
	;;
esac

crun --version
