package catalog

import (
	"fmt"

	"github.com/maryzam/ai-crew-localdev/internal/broker/port"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/identity"
	"github.com/maryzam/ai-crew-localdev/internal/interception"
	"github.com/maryzam/ai-crew-localdev/internal/providers/github"
	githubcontract "github.com/maryzam/ai-crew-localdev/internal/providers/github/contract"
	"github.com/maryzam/ai-crew-localdev/internal/providers/langfuse"
	langfusecontract "github.com/maryzam/ai-crew-localdev/internal/providers/langfuse/contract"
)

func Providers(identities *identity.IdentitiesFile, githubBaseURL string) ([]port.Provider, error) {
	signer, err := github.NewSigner(identities)
	if err != nil {
		return nil, fmt.Errorf("create signer: %w", err)
	}
	githubProvider := github.New(github.NewGitHubClient(githubBaseURL), signer, appIDResolver(identities))
	return []port.Provider{githubProvider, langfuse.New()}, nil
}

func InterceptionProfiles() []interception.Profile {
	return []interception.Profile{
		githubcontract.InterceptionProfile(),
		langfusecontract.InterceptionProfile(),
	}
}

func appIDResolver(identities *identity.IdentitiesFile) func(agent string) string {
	return func(agent string) string {
		if a, ok := identities.Agents[agent]; ok {
			return a.AppID
		}
		return ""
	}
}
