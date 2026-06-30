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
	unexpected=$(go list -buildvcs=false -f '{{range .Imports}}{{println .}}{{end}}' $packages | grep -E "^${module}/internal/providers/" | grep -Ev "^${module}/internal/providers/(github|langfuse)/contract$" || true)
	if [ -n "$unexpected" ]; then
		printf 'dependency boundary failed: %s\n%s\n' "$label" "$unexpected" >&2
		failed=1
	fi
}

reject_imports "./internal/brokerapi" "^${module}/internal/" "brokerapi must not depend on implementation packages"
reject_imports "./internal/brokerport" "^${module}/internal/(broker|providers)($|/)" "brokerport may depend on brokerapi but not broker core or providers"
reject_imports "./internal/brokerclient" "^${module}/internal/(broker|providers)($|/)" "brokerclient may depend on brokerapi but not broker core or providers"
reject_imports "./internal/broker" "^${module}/internal/providers/" "broker core must not depend on provider implementations"
reject_imports "./internal/providers/..." "^${module}/internal/broker$" "providers must depend on broker ports instead of broker core"
reject_imports "./internal/providers/github/contract ./internal/providers/langfuse/contract" "^${module}/internal/" "provider contracts must not depend on implementation packages"
allow_provider_contracts "./internal/cli" "CLI may import provider contracts only; concrete services belong in an executable composition root"
allow_internal_imports "./cmd/ai-agent-gh ./cmd/ai-agent-credential-helper" "^${module}/internal/(brokerapi|brokerclient|sessionauth|providers/github/contract)$" "wrappers may depend only on transport, session authentication, and payload contracts"
reject_imports "./internal/onboarding ./internal/readiness" "(^github.com/spf13/cobra$|^${module}/internal/cli$)" "application workflows must not depend on Cobra or CLI adapters"
reject_imports "./internal/uphost ./internal/devcontainer" "(^github.com/spf13/cobra$|^${module}/internal/cli$)" "host and devcontainer adapters must not depend on Cobra or CLI packages"

exit "$failed"
