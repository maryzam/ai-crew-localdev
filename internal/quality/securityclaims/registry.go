package securityclaims

import (
	"bytes"
	"fmt"
	"os"
	"strings"
)

const (
	DocPath               = "docs/design/security-design.md"
	UserSecurityDocPath   = "docs/guide/security-for-users.md"
	UserManualDocPath     = "docs/guide/user-manual.md"
	InvariantsBeginMarker = "<!-- BEGIN generated: security-invariants (regenerate with `make security-claims`) -->"
	InvariantsEndMarker   = "<!-- END generated: security-invariants -->"
	NonGoalsBeginMarker   = "<!-- BEGIN generated: security-non-goals (regenerate with `make security-claims`) -->"
	NonGoalsEndMarker     = "<!-- END generated: security-non-goals -->"
)

type Invariant struct {
	ID          int
	Claim       string
	Enforcement string
	Proofs      []Proof
}

type Proof struct {
	Path string
	Test string
}

type NonGoal struct {
	Label     string
	Text      string
	UserLabel string
	UserText  string
}

func Invariants() []Invariant {
	return cloneInvariants(invariants)
}

func NonGoals() []NonGoal {
	return append([]NonGoal(nil), nonGoals...)
}

func Markdown() string {
	var b strings.Builder
	b.WriteString(InvariantsBeginMarker)
	b.WriteString("\n| # | Invariant | Enforced by |\n")
	b.WriteString("|---|-----------|-------------|\n")
	for _, invariant := range invariants {
		_, _ = fmt.Fprintf(&b, "| %d | %s | %s Proof: %s. |\n", invariant.ID, invariant.Claim, invariant.Enforcement, proofList(invariant.Proofs))
	}
	b.WriteString(InvariantsEndMarker)
	b.WriteString("\n")
	return b.String()
}

func NonGoalsMarkdown() string {
	return nonGoalsMarkdown(false)
}

func UserNonGoalsMarkdown() string {
	return nonGoalsMarkdown(true)
}

func nonGoalsMarkdown(userFacing bool) string {
	var b strings.Builder
	b.WriteString(NonGoalsBeginMarker)
	b.WriteString("\n")
	for _, nonGoal := range nonGoals {
		label := nonGoal.Label
		text := nonGoal.Text
		if userFacing {
			if nonGoal.UserLabel != "" {
				label = nonGoal.UserLabel
			}
			if nonGoal.UserText != "" {
				text = nonGoal.UserText
			}
		}
		_, _ = fmt.Fprintf(&b, "- **%s.** %s\n", label, text)
	}
	b.WriteString(NonGoalsEndMarker)
	b.WriteString("\n")
	return b.String()
}

func CheckAllDocuments() error {
	for _, block := range generatedBlocks() {
		if err := checkBlock(block); err != nil {
			return err
		}
	}
	return nil
}

func UpdateAllDocuments() error {
	for _, block := range generatedBlocks() {
		if err := updateBlock(block); err != nil {
			return err
		}
	}
	return nil
}

func CheckDocument(path string) error {
	blocks := blocksForPath(path)
	if len(blocks) == 0 {
		return fmt.Errorf("no generated security claims registered for %s", path)
	}
	for _, block := range blocks {
		if err := checkBlock(block); err != nil {
			return err
		}
	}
	return nil
}

func UpdateDocument(path string) error {
	blocks := blocksForPath(path)
	if len(blocks) == 0 {
		return fmt.Errorf("no generated security claims registered for %s", path)
	}
	for _, block := range blocks {
		if err := updateBlock(block); err != nil {
			return err
		}
	}
	return nil
}

type generatedBlock struct {
	Path        string
	BeginMarker string
	EndMarker   string
	Markdown    string
}

func generatedBlocks() []generatedBlock {
	return []generatedBlock{
		{Path: DocPath, BeginMarker: InvariantsBeginMarker, EndMarker: InvariantsEndMarker, Markdown: Markdown()},
		{Path: DocPath, BeginMarker: NonGoalsBeginMarker, EndMarker: NonGoalsEndMarker, Markdown: NonGoalsMarkdown()},
		{Path: UserSecurityDocPath, BeginMarker: NonGoalsBeginMarker, EndMarker: NonGoalsEndMarker, Markdown: UserNonGoalsMarkdown()},
		{Path: UserManualDocPath, BeginMarker: NonGoalsBeginMarker, EndMarker: NonGoalsEndMarker, Markdown: UserNonGoalsMarkdown()},
	}
}

func blocksForPath(path string) []generatedBlock {
	var blocks []generatedBlock
	for _, block := range generatedBlocks() {
		if block.Path == path {
			blocks = append(blocks, block)
		}
	}
	return blocks
}

func checkBlock(block generatedBlock) error {
	current, err := os.ReadFile(block.Path)
	if err != nil {
		return err
	}
	updated, err := replaceGeneratedBlock(current, block.BeginMarker, block.EndMarker, []byte(block.Markdown))
	if err != nil {
		return err
	}
	if !bytes.Equal(current, updated) {
		return fmt.Errorf("%s is stale; run make security-claims", block.Path)
	}
	return nil
}

func updateBlock(block generatedBlock) error {
	current, err := os.ReadFile(block.Path)
	if err != nil {
		return err
	}
	updated, err := replaceGeneratedBlock(current, block.BeginMarker, block.EndMarker, []byte(block.Markdown))
	if err != nil {
		return err
	}
	return os.WriteFile(block.Path, updated, 0o644)
}

func replaceGeneratedBlock(document []byte, beginMarker string, endMarker string, replacement []byte) ([]byte, error) {
	text := string(document)
	begin := strings.Index(text, beginMarker)
	if begin < 0 {
		return nil, fmt.Errorf("%s marker missing", beginMarker)
	}
	end := strings.Index(text[begin:], endMarker)
	if end < 0 {
		return nil, fmt.Errorf("%s marker missing", endMarker)
	}
	end += begin + len(endMarker)
	if end < len(text) && text[end] == '\r' {
		end++
	}
	if end < len(text) && text[end] == '\n' {
		end++
	}
	result := make([]byte, 0, len(document)-end+begin+len(replacement))
	result = append(result, document[:begin]...)
	result = append(result, replacement...)
	result = append(result, document[end:]...)
	return result, nil
}

func proofList(proofs []Proof) string {
	parts := make([]string, 0, len(proofs))
	for _, proof := range proofs {
		parts = append(parts, fmt.Sprintf("`%s:%s`", proof.Path, proof.Test))
	}
	return strings.Join(parts, ", ")
}

func cloneInvariants(input []Invariant) []Invariant {
	output := make([]Invariant, len(input))
	for i, invariant := range input {
		output[i] = invariant
		output[i].Proofs = append([]Proof(nil), invariant.Proofs...)
	}
	return output
}

var invariants = []Invariant{
	{
		ID:          1,
		Claim:       "Durable provider secrets stay in broker/provider-owned code and are not returned through workspace credential APIs.",
		Enforcement: "GitHub signing is provider-side, Langfuse egress uses durable keys only in the provider, and telemetry/session wire contracts omit provider secret fields.",
		Proofs: []Proof{
			{Path: "internal/providers/github/signer_test.go", Test: "TestSignJWT"},
			{Path: "internal/providers/langfuse/provider_test.go", Test: "TestProviderPublishesWithDurableSecretOnlyInUpstreamAuthorization"},
			{Path: "internal/broker/api/api_contract_test.go", Test: "TestPublishTelemetryWireShapeHasNoProviderCredentialFields"},
		},
	},
	{
		ID:          2,
		Claim:       "GitHub credentials minted for a run are installation tokens scoped by repository and requested permissions.",
		Enforcement: "The GitHub provider validates permission subsets, rejects escalation, and delegates short-lived installation-token minting to GitHub.",
		Proofs: []Proof{
			{Path: "internal/providers/github/provider_test.go", Test: "TestProviderMintDownscope"},
			{Path: "internal/providers/github/provider_test.go", Test: "TestProviderMintRejectsEscalation"},
		},
	},
	{
		ID:          3,
		Claim:       "Each broker session is bound to declared resources, and cross-resource credential requests are denied.",
		Enforcement: "Session creation records parsed resources and credential minting rechecks the requested resource against the session before provider work begins.",
		Proofs: []Proof{
			{Path: "internal/broker/core/session_resources_test.go", Test: "TestMemorySessionStoreCreateResources"},
			{Path: "internal/broker/core/server_mint_credential_test.go", Test: "TestBrokerMintCredentialResourceNotInSession"},
		},
	},
	{
		ID:          4,
		Claim:       "Brokered git and gh paths fail closed instead of falling back to ambient personal credentials.",
		Enforcement: "The launcher forces non-interactive git credentials, installs only the broker credential helper for git, and wrappers require managed-session state.",
		Proofs: []Proof{
			{Path: "internal/runtime/launcher/scrub_invariants_test.go", Test: "TestScrubEnvDisablesInteractiveGitCredentials"},
			{Path: "internal/runtime/launcher/scrub_invariants_test.go", Test: "TestScrubEnvUsesOnlyBrokerCredentialHelper"},
			{Path: "test/e2e/project_devcontainer_test.go", Test: "TestProjectDevcontainerE2E"},
		},
	},
	{
		ID:          5,
		Claim:       "Ambient provider credentials are scrubbed from every managed agent process.",
		Enforcement: "Provider interception profiles declare credential environment names and prefixes, and the launcher applies the union before exec.",
		Proofs: []Proof{
			{Path: "internal/runtime/launcher/scrub_invariants_test.go", Test: "TestEveryProfileScrubsItsAmbientCredentials"},
			{Path: "test/e2e/project_devcontainer_test.go", Test: "TestProjectDevcontainerE2E"},
		},
	},
	{
		ID:          6,
		Claim:       "Credential issuance is withheld unless durable audit intent is recorded.",
		Enforcement: "The broker writes audit records synchronously before credential mint success and latches storage failure into broker health.",
		Proofs: []Proof{
			{Path: "internal/broker/core/server_audit_test.go", Test: "TestBrokerDoesNotMintWithoutDurableAuditIntent"},
			{Path: "internal/broker/core/fileaudit_test.go", Test: "TestFileAuditLoggerPersistsBeforeRecordReturns"},
		},
	},
	{
		ID:          7,
		Claim:       "The broker validates Unix peer credentials for every socket connection.",
		Enforcement: "The accept path reads `SO_PEERCRED` before decoding a request and rejects peers outside the allowed UID boundary.",
		Proofs: []Proof{
			{Path: "internal/broker/core/peercred_test.go", Test: "TestPeerCred"},
		},
	},
	{
		ID:          8,
		Claim:       "Token minting requires the per-session binding secret carried by sealed memfd, not environment or disk state.",
		Enforcement: "The launcher creates a sealed bind fd, passes it to the child as fd 3, and the broker validates the secret on credential and telemetry requests.",
		Proofs: []Proof{
			{Path: "internal/runtime/launcher/memfd_test.go", Test: "TestCreateBindFDIsSealed"},
			{Path: "internal/runtime/launcher/launcher_test.go", Test: "TestLaunchPassesBindFDToAgent"},
			{Path: "internal/broker/core/server_mint_credential_test.go", Test: "TestBrokerMintCredentialBindingMismatch"},
		},
	},
	{
		ID:          9,
		Claim:       "Generic devcontainers mount only the workspace, broker socket directory, and persistent agent home.",
		Enforcement: "The checked-in generic devcontainer asset is the embedded release asset and its mount list is parity-checked.",
		Proofs: []Proof{
			{Path: "internal/runtime/devcontainer/assets/assets_test.go", Test: "TestEmbeddedGenericAssetsMatchCheckout"},
			{Path: "internal/runtime/devcontainer/assets/assets_test.go", Test: "TestGenericDevcontainerDeclaresOnlyManagedMounts"},
		},
	},
	{
		ID:          10,
		Claim:       "Broker policy is the authority for provider resources; shims and manifests cannot grant credentials by themselves.",
		Enforcement: "The planner performs broker-authoritative resource preflight and the broker reauthorizes resources at session creation and credential mint.",
		Proofs: []Proof{
			{Path: "internal/control/planner_test.go", Test: "TestPlannerIncludesManifestResourcesAndResourceBudgets"},
			{Path: "internal/broker/core/server_test.go", Test: "TestBrokerCreateSessionDisallowedResource"},
			{Path: "internal/broker/core/server_safety_test.go", Test: "TestBrokerMintAfterReloadRemovingResourceIsRejected"},
		},
	},
	{
		ID:          11,
		Claim:       "The generic devcontainer declares dropped capabilities, no-new-privileges, and a read-only root filesystem.",
		Enforcement: "The devcontainer runtime args are checked in the canonical asset and parity-checked against the embedded release asset.",
		Proofs: []Proof{
			{Path: "internal/runtime/devcontainer/assets/assets_test.go", Test: "TestGenericDevcontainerDeclaresConfinementArgs"},
			{Path: "internal/runtime/devcontainer/assets/assets_test.go", Test: "TestEmbeddedGenericAssetsMatchCheckout"},
		},
	},
	{
		ID:          12,
		Claim:       "PEM private keys must be owner-only regular files before the broker will load them.",
		Enforcement: "Doctor and broker loading share `securefile` rather than reimplementing PEM file validation.",
		Proofs: []Proof{
			{Path: "internal/app/readiness/securefile_parity_test.go", Test: "TestDoctorPEMVerdictMatchesBrokerAcceptance"},
			{Path: "internal/quality/boundaries/boundaries_test.go", Test: "TestReadinessDefersSecureFileValidation"},
		},
	},
	{
		ID:          13,
		Claim:       "Credential-writing `gh auth` commands are rejected on the supported brokered path.",
		Enforcement: "`ai-agent-gh` blocks login, setup-git, and refresh before requesting a broker credential or invoking real gh.",
		Proofs: []Proof{
			{Path: "internal/shim/ghwrapper/ghwrapper_test.go", Test: "TestRejectPersistentAuthCommand"},
			{Path: "test/e2e/project_devcontainer_test.go", Test: "TestProjectDevcontainerE2E"},
		},
	},
	{
		ID:          14,
		Claim:       "Runtime assets come from embedded trusted sources by default, not the ambient working directory.",
		Enforcement: "Generic devcontainer and Langfuse staging resolve through explicit asset sources, with checkout overrides gated behind a development environment variable.",
		Proofs: []Proof{
			{Path: "internal/quality/boundaries/boundaries_test.go", Test: "TestAssetResolutionNeverTrustsWorkingDirectory"},
			{Path: "internal/runtime/devcontainer/assets/assets_test.go", Test: "TestGenericImageBuildsFromStagedBinaryNotSource"},
		},
	},
	{
		ID:          15,
		Claim:       "Doctor readiness reports the same PEM acceptance boundary the broker will enforce.",
		Enforcement: "Readiness delegates to `securefile`, and parity tests compare doctor verdicts with broker acceptance across adversarial fixtures.",
		Proofs: []Proof{
			{Path: "internal/app/readiness/securefile_parity_test.go", Test: "TestDoctorPEMVerdictMatchesBrokerAcceptance"},
			{Path: "internal/quality/boundaries/boundaries_test.go", Test: "TestReadinessDefersSecureFileValidation"},
		},
	},
	{
		ID:          16,
		Claim:       "Accidental host-native managed runs are rejected before brokered work begins on the current supported path.",
		Enforcement: "The planner and launcher call the shared managed-runtime guard before helper resolution, broker setup, or launch side effects, and a boundary test prevents new direct marker readers; this is an operator guardrail, not a kernel identity boundary.",
		Proofs: []Proof{
			{Path: "internal/platform/runenv/runenv_test.go", Test: "TestRequireManagedContainerRejectsMissingMarker"},
			{Path: "internal/quality/boundaries/boundaries_test.go", Test: "TestManagedRuntimeMarkerHasOneReader"},
			{Path: "internal/control/planner_test.go", Test: "TestPlannerRejectsNativeHostRunBeforeHelperResolution"},
			{Path: "internal/runtime/launcher/launcher_test.go", Test: "TestLaunchRejectsMissingDevcontainerMarkerBeforeBroker"},
		},
	},
}

var nonGoals = []NonGoal{
	{
		Label:    "Single-user workstation only",
		Text:     "Same-UID processes on the workstation can reach the broker socket; this is not a multi-tenant sandbox.",
		UserText: "A fully compromised user account or operating system can reach the broker socket; this is a single-user workstation tool, not a multi-tenant service.",
	},
	{
		Label:    "Not adversarial process containment",
		Text:     "The supported path rejects accidental host-native managed runs and keeps durable credentials behind the broker, but a process that can spoof the devcontainer marker, make raw network calls, reach absolute host paths made available by the workspace or a custom image, or compromise the same UID is outside the containment claim.",
		UserText: "The tool protects brokered credentials on the managed path; it does not turn the container into a general-purpose sandbox for hostile local processes, arbitrary network access, or files exposed by your workspace or custom image.",
	},
	{
		Label:     "The `gh` wrapper covers the supported command path, not a sandbox boundary",
		Text:      "A process that invokes a real `gh` by absolute path, or makes raw network calls, is not stopped by the wrapper.",
		UserLabel: "Managed commands only",
		UserText:  "Use managed `git` and `gh` commands for repository access; commands that intentionally bypass the managed path are outside the credential guarantees.",
	},
	{
		Label:    "HTTPS remotes only",
		Text:     "SSH git operations are not supported.",
		UserText: "SSH git remotes are unsupported; use HTTPS remotes.",
	},
	{
		Label:    "Linux only",
		Text:     "Phase 1 supports Linux hosts.",
		UserText: "Non-Linux hosts are not supported yet.",
	},
	{
		Label:    "Agent login-state checks are local",
		Text:     "`ai-agent up` login status and login-state tests prove persistence and local recognition, not a provider-backed authenticated request.",
		UserText: "`ai-agent up` can recognize saved agent CLI login state, but that local check is not a provider-backed authenticated request.",
	},
}
