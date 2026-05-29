#!/usr/bin/env bash
set -euo pipefail

ROOT="$(git rev-parse --show-toplevel)"
cd "$ROOT"

ACTIVE_IDENTIFIERS=(
  mint_credential
  create_session
  revoke_session
  session_status
  health_check
  session_not_found
  session_expired
  binding_mismatch
  resource_not_allowed
  permission_denied
  uid_mismatch
  rate_limited
  broker_unavailable
  upstream_error
  unknown_credential_type
  invalid_resource_uri
  github_app_installation
)

LEGACY_IDENTIFIERS=(
  allowed_repos
  mint_token
  repo_not_allowed
)

LEGACY_ALLOWLIST=(
  "docs/decisions/0001-credential-generic-broker-api.md:allowed_repos"
  "docs/decisions/0001-credential-generic-broker-api.md:mint_token"
  "docs/proposals/quality-gates.md:allowed_repos"
  "docs/proposals/quality-gates.md:repo_not_allowed"
)

is_legacy_allowed() {
  local file="$1"
  local ident="$2"
  local allowed
  for allowed in "${LEGACY_ALLOWLIST[@]}"; do
    if [[ "$allowed" == "$file:$ident" ]]; then
      return 0
    fi
  done
  return 1
}

status=0

for ident in "${ACTIVE_IDENTIFIERS[@]}"; do
  if ! rg -Fq "$ident" internal/broker internal/brokerclient; then
    printf 'active broker identifier %q is documented as allowed but was not found in broker code\n' "$ident" >&2
    status=1
  fi
done

mapfile -t docs < <(find docs -type f -name '*.md' -print | sort)
docs+=(README.md)

for ident in "${LEGACY_IDENTIFIERS[@]}"; do
  while IFS=: read -r file line text; do
    if is_legacy_allowed "$file" "$ident"; then
      continue
    fi
    printf '%s:%s: stale broker identifier %q found: %s\n' "$file" "$line" "$ident" "$text" >&2
    status=1
  done < <(rg -n -F "$ident" "${docs[@]}" || true)
done

if (( status != 0 )); then
  printf '\nUpdate the docs to use current broker wire identifiers, or add a narrow historical exception near LEGACY_ALLOWLIST.\n' >&2
fi

exit "$status"
