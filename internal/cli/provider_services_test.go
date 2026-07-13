package cli

import (
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/identity"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/policy"
	"github.com/maryzam/ai-crew-localdev/internal/governance/policycheck"
)

func testPolicyValidator(policyFile *policy.PolicyFile, identities *identity.IdentitiesFile) error {
	return policycheck.Validate(policyFile, identities)
}
