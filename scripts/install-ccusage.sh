#!/usr/bin/env bash
set -euo pipefail

version="${CCUSAGE_VERSION:-20.0.14}"
target="${1:-bin/ccusage}"

case "$(uname -m)" in
    x86_64 | amd64)
        package_arch="x64"
        expected_sha256="dfcd0ea98fc56d71cff77db000d307b011fe218333ac93f7697d242e1f587e35"
        ;;
    aarch64 | arm64)
        package_arch="arm64"
        expected_sha256="f1f7e21073f17905a02daa31cc2b117e39fe10aae18454b228dec04a11a5d1de"
        ;;
    *)
        echo >&2 "install-ccusage: unsupported Linux architecture $(uname -m)"
        exit 1
        ;;
esac

install_verified() {
    local source="$1"
    [[ -f "$source" ]] || return 1
    [[ "$(sha256sum "$source" | awk '{print $1}')" == "$expected_sha256" ]] || return 1
    mkdir -p "$(dirname "$target")"
    install -m 0755 "$source" "$target"
    if [[ "$("$target" --version)" != "ccusage $version" ]]; then
        rm -f "$target"
        return 1
    fi
}

if [[ -n "${CCUSAGE_NATIVE_SOURCE:-}" ]]; then
    install_verified "$CCUSAGE_NATIVE_SOURCE" || {
        echo >&2 "install-ccusage: supplied binary has the wrong version"
        exit 1
    }
    exit 0
fi

command -v npm >/dev/null 2>&1 || {
    echo >&2 "install-ccusage: npm is not installed"
    exit 1
}

global_candidate="$(npm root -g 2>/dev/null || true)/@ccusage/ccusage-linux-${package_arch}/bin/ccusage"
if install_verified "$global_candidate"; then
    exit 0
fi

stage="$(mktemp -d)"
trap 'rm -rf "$stage"' EXIT
npm install --prefix "$stage" --fetch-retries 1 --fetch-timeout 10000 --ignore-scripts --no-audit --no-fund --no-package-lock --no-save --prefer-offline "ccusage@${version}"

source="$stage/node_modules/@ccusage/ccusage-linux-${package_arch}/bin/ccusage"
install_verified "$source" || {
    echo >&2 "install-ccusage: native binary missing or invalid"
    exit 1
}
