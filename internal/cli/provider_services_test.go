package cli

import (
	"github.com/maryzam/ai-crew-localdev/internal/broker/policycheck"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/identity"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/policy"
)

func testPolicyValidator(policyFile *policy.PolicyFile, identities *identity.IdentitiesFile) error {
	return policycheck.Validate(policyFile, identities)
}
