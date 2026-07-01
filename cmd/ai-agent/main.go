package main

import (
	"os"

	"github.com/maryzam/ai-crew-localdev/internal/broker/core"
	"github.com/maryzam/ai-crew-localdev/internal/broker/port"
	"github.com/maryzam/ai-crew-localdev/internal/cli"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/identity"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/policy"
	githubprovider "github.com/maryzam/ai-crew-localdev/internal/providers/github"
	langfuseprovider "github.com/maryzam/ai-crew-localdev/internal/providers/langfuse"
)

func main() {
	githubClient := githubprovider.NewGitHubClient("")
	services := cli.ProviderServices{
		GitHubClient: githubClient,
		NewSigner: func(identities *identity.IdentitiesFile) (cli.JWTSigner, error) {
			return githubprovider.NewSigner(identities)
		},
		ValidatePolicy: func(policyFile *policy.PolicyFile, identities *identity.IdentitiesFile) error {
			providers := []port.CredentialProvider{
				githubprovider.NewValidator(identityAppIDResolver(identities)),
				langfuseprovider.New(),
			}
			return core.ValidatePolicy(policyFile, providers)
		},
	}
	if err := cli.Execute(services); err != nil {
		os.Exit(1)
	}
}

func identityAppIDResolver(identities *identity.IdentitiesFile) func(string) string {
	return func(agent string) string {
		if identities == nil {
			return ""
		}
		return identities.Agents[agent].AppID
	}
}
