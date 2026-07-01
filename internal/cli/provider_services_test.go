package cli

import (
	"github.com/maryzam/ai-crew-localdev/internal/broker/core"
	"github.com/maryzam/ai-crew-localdev/internal/broker/port"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/identity"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/policy"
	githubprovider "github.com/maryzam/ai-crew-localdev/internal/providers/github"
	langfuseprovider "github.com/maryzam/ai-crew-localdev/internal/providers/langfuse"
)

func testPolicyValidator(policyFile *policy.PolicyFile, identities *identity.IdentitiesFile) error {
	resolver := func(agent string) string {
		if identities == nil {
			return ""
		}
		return identities.Agents[agent].AppID
	}
	providers := []port.CredentialProvider{githubprovider.NewValidator(resolver), langfuseprovider.New()}
	return core.ValidatePolicy(policyFile, providers)
}
