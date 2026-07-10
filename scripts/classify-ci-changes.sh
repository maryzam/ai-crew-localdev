#!/usr/bin/env bash
set -euo pipefail

write_outputs() {
	local docs_changed="$1"
	local full_verify="$2"
	local readiness="$3"
	if [ -n "${GITHUB_OUTPUT:-}" ]; then
		{
			printf 'docs_changed=%s\n' "$docs_changed"
			printf 'full_verify=%s\n' "$full_verify"
			printf 'readiness=%s\n' "$readiness"
		} >>"$GITHUB_OUTPUT"
	else
		printf 'docs_changed=%s\n' "$docs_changed"
		printf 'full_verify=%s\n' "$full_verify"
		printf 'readiness=%s\n' "$readiness"
	fi
}

fail_closed() {
	local readiness=false
	if [ "${EVENT_NAME:-}" = "pull_request" ]; then
		readiness=true
	fi
	write_outputs true true "$readiness"
	printf 'changed path classification failed closed: %s\n' "$1" >&2
	exit 0
}

ensure_commit() {
	local sha="$1"
	[ -n "$sha" ] || return 1
	case "$sha" in
		0000000000000000000000000000000000000000) return 1 ;;
	esac
	if git cat-file -e "${sha}^{commit}" 2>/dev/null; then
		return 0
	fi
	git fetch --no-tags --depth=1 origin "$sha" >/dev/null 2>&1 || return 1
	git cat-file -e "${sha}^{commit}" 2>/dev/null
}

ensure_commit "${BASE_SHA:-}" || fail_closed "base commit is unavailable"
ensure_commit "${HEAD_SHA:-}" || fail_closed "head commit is unavailable"

mapfile -t files < <(git diff --name-only --diff-filter=ACMRTD "$BASE_SHA" "$HEAD_SHA")
docs_changed=false
docs_only=true

if [ "${#files[@]}" -eq 0 ]; then
	docs_only=false
fi

for file in "${files[@]}"; do
	case "$file" in
		README.md|docs/*)
			docs_changed=true
			;;
		*)
			docs_only=false
			;;
	esac
done

if [ "$docs_only" = "true" ]; then
	full_verify=false
else
	full_verify=true
fi

if [ "${EVENT_NAME:-}" = "pull_request" ] && [ "$docs_only" != "true" ]; then
	readiness=true
else
	readiness=false
fi

write_outputs "$docs_changed" "$full_verify" "$readiness"

printf 'Changed files:\n'
printf '  %s\n' "${files[@]}"
printf 'docs_changed=%s full_verify=%s readiness=%s\n' "$docs_changed" "$full_verify" "$readiness"
