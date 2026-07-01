#!/usr/bin/env bash
set -euo pipefail

usage() {
	printf '%s\n' "usage:"
	printf '%s\n' "  $0 --cached --message-file <path>"
	printf '%s\n' "  $0 --range <base> <head> --body-file <path>"
}

die() {
	printf 'adr-gate: %s\n' "$*" >&2
	exit 1
}

require_file() {
	if [ ! -f "$1" ]; then
		die "file not found: $1"
	fi
}

contains_no_adr_raw() {
	grep -Fq '[no-adr]' "$1"
}

contains_no_adr_commit_message() {
	awk '
		/^[[:space:]]*#/ { next }
		index($0, "[no-adr]") { found = 1 }
		END { exit found ? 0 : 1 }
	' "$1"
}

has_added_adr() {
	awk '
		$1 == "A" && $2 ~ /^docs\/decisions\/.+\.md$/ { found = 1 }
		$1 ~ /^[CR][0-9]*$/ && $3 ~ /^docs\/decisions\/.+\.md$/ { found = 1 }
		END { exit found ? 0 : 1 }
	'
}

has_risky_path_changes() {
	awk '
		{
			for (i = 2; i <= NF; i++) {
				if ($i ~ /^internal\/broker\// || $i ~ /^internal\/configmodel\/policy\//) {
					found = 1
				}
			}
		}
		END { exit found ? 0 : 1 }
	'
}

has_credential_provider_changes() {
	awk '
		/^diff --git / { in_go = ($0 ~ /\.go( |$)/) }
		in_go && /^[+-]/ && !/^(---|\+\+\+)/ && /CredentialProvider/ { found = 1 }
		END { exit found ? 0 : 1 }
	'
}

mode=
message_file=
body_file=
base_ref=
head_ref=

while [ "$#" -gt 0 ]; do
	case "$1" in
		--cached)
			mode=cached
			shift
			;;
		--message-file)
			[ "$#" -ge 2 ] || die "--message-file requires a path"
			message_file=$2
			shift 2
			;;
		--range)
			[ "$#" -ge 3 ] || die "--range requires base and head refs"
			mode=range
			base_ref=$2
			head_ref=$3
			shift 3
			;;
		--body-file)
			[ "$#" -ge 2 ] || die "--body-file requires a path"
			body_file=$2
			shift 2
			;;
		-h|--help)
			usage
			exit 0
			;;
		*)
			usage >&2
			die "unknown argument: $1"
			;;
	esac
done

name_status_file=$(mktemp)
patch_file=$(mktemp)
trap 'rm -f "$name_status_file" "$patch_file"' EXIT

case "$mode" in
	cached)
		[ -n "$message_file" ] || die "--cached requires --message-file"
		require_file "$message_file"
		git diff --cached --name-status --find-renames >"$name_status_file"
		git diff --cached >"$patch_file"
		token_file=$message_file
		token_source=commit-message
		scope='staged changes'
		;;
	range)
		[ -n "$base_ref" ] || die "--range requires a base ref"
		[ -n "$head_ref" ] || die "--range requires a head ref"
		[ -n "$body_file" ] || die "--range requires --body-file"
		require_file "$body_file"
		git rev-parse --verify "${base_ref}^{commit}" >/dev/null
		git rev-parse --verify "${head_ref}^{commit}" >/dev/null
		git diff --name-status --find-renames "$base_ref...$head_ref" >"$name_status_file"
		git diff "$base_ref...$head_ref" >"$patch_file"
		token_file=$body_file
		token_source=pr-body
		scope='PR combined diff'
		;;
	*)
		usage >&2
		die "missing --cached or --range"
		;;
esac

if ! has_risky_path_changes <"$name_status_file" && ! has_credential_provider_changes <"$patch_file"; then
	printf 'adr-gate: no ADR required for %s\n' "$scope"
	exit 0
fi

if [ "$token_source" = "commit-message" ] && contains_no_adr_commit_message "$token_file"; then
	printf 'adr-gate: [no-adr] opt-out found for %s\n' "$scope"
	exit 0
fi

if [ "$token_source" = "pr-body" ] && contains_no_adr_raw "$token_file"; then
	printf 'adr-gate: [no-adr] opt-out found for %s\n' "$scope"
	exit 0
fi

if has_added_adr <"$name_status_file"; then
	printf 'adr-gate: docs/decisions ADR addition found for %s\n' "$scope"
	exit 0
fi

cat >&2 <<'EOF'
adr-gate: high-risk broker/policy/provider changes require an ADR.

Add a new docs/decisions/**/*.md file, or include [no-adr] in the commit
message or PR body when the change does not need a decision record.
EOF
exit 1
