package policycheck

import (
	"github.com/maryzam/ai-crew-localdev/internal/broker/core"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/identity"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/policy"
	"github.com/maryzam/ai-crew-localdev/internal/providers/catalog"
)

func Validate(policyFile *policy.PolicyFile, identities *identity.IdentitiesFile) error {
	providers, err := catalog.Validators(identities)
	if err != nil {
		return err
	}
	return core.ValidatePolicy(policyFile, providers)
}
