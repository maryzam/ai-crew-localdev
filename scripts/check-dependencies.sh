#!/usr/bin/env bash
set -euo pipefail

module="github.com/maryzam/ai-crew-localdev"
failed=0

reject_imports() {
	local packages=$1
	local forbidden=$2
	local label=$3
	local imports
	imports=$(go list -buildvcs=false -f '{{range .Imports}}{{println .}}{{end}}' $packages)
	if printf '%s\n' "$imports" | grep -Eq "$forbidden"; then
		printf 'dependency boundary failed: %s\n' "$label" >&2
		printf '%s\n' "$imports" | grep -E "$forbidden" >&2
		failed=1
	fi
}

allow_internal_imports() {
	local packages=$1
	local allowed=$2
	local label=$3
	local unexpected
	unexpected=$(go list -buildvcs=false -f '{{range .Imports}}{{println .}}{{end}}' $packages | grep -E "^${module}/internal/" | grep -Ev "$allowed" || true)
	if [ -n "$unexpected" ]; then
		printf 'dependency boundary failed: %s\n%s\n' "$label" "$unexpected" >&2
		failed=1
	fi
}

allow_provider_contracts() {
	local packages=$1
	local label=$2
	local unexpected
	unexpected=$(go list -buildvcs=false -f '{{range .Imports}}{{println .}}{{end}}' $packages | grep -E "^${module}/internal/providers/" | grep -Ev "^${module}/internal/providers/([a-z]+/contract|profiles)$" || true)
	if [ -n "$unexpected" ]; then
		printf 'dependency boundary failed: %s\n%s\n' "$label" "$unexpected" >&2
		failed=1
	fi
}

reject_imports "./internal/broker/api" "^${module}/internal/" "broker/api must not depend on implementation packages"
reject_imports "./internal/interception" "^${module}/internal/" "interception profile types must not depend on implementation packages"
allow_internal_imports "./internal/broker/port ./internal/broker/client" "^${module}/internal/broker/api$" "broker/port and broker/client may depend only on broker/api"
reject_imports "./internal/broker/core" "^${module}/internal/providers/" "broker core must not depend on provider implementations"
reject_imports "./internal/providers/..." "^${module}/internal/broker/core$" "providers must depend on broker ports instead of broker core"
contract_packages=$(go list -buildvcs=false ./internal/providers/... | grep -E '/contract$' || true)
if [ -z "$contract_packages" ]; then
	printf 'dependency boundary failed: no provider contract packages found under internal/providers\n' >&2
	failed=1
else
	allow_internal_imports "$contract_packages" "^${module}/internal/interception$" "provider contracts may depend only on interception profile types"
fi
allow_internal_imports "./internal/providers/profiles" "^${module}/internal/(interception|providers/[a-z]+/contract)$" "the profile registry may depend only on interception types and provider contracts"
allow_provider_contracts "./internal/cli" "CLI may import provider contracts only; concrete services belong in an executable composition root"
allow_provider_contracts "./internal/runtime/..." "runtime adapters may import provider contracts only, never provider implementations"
allow_internal_imports "./internal/shim/..." "^${module}/internal/(broker/api|broker/client|runtime/session|providers/github/contract)$" "shims may depend only on transport, session authentication, and payload contracts"
reject_imports "./internal/app/... ./internal/runtime/..." "(^github.com/spf13/cobra$|^${module}/internal/cli$)" "application workflows and runtime adapters must not depend on Cobra or CLI packages"
allow_internal_imports "./internal/platform/..." "^${module}/internal/platform/" "platform primitives must not depend on any internal package outside internal/platform"

exit "$failed"
