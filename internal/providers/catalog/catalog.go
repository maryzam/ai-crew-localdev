package catalog

import (
	"fmt"

	"github.com/maryzam/ai-crew-localdev/internal/broker/port"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/identity"
	"github.com/maryzam/ai-crew-localdev/internal/interception"
	"github.com/maryzam/ai-crew-localdev/internal/providers/github"
	"github.com/maryzam/ai-crew-localdev/internal/providers/langfuse"
	"github.com/maryzam/ai-crew-localdev/internal/providers/profiles"
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
	return profiles.All()
}

func appIDResolver(identities *identity.IdentitiesFile) func(agent string) string {
	return func(agent string) string {
		if a, ok := identities.Agents[agent]; ok {
			return a.AppID
		}
		return ""
	}
}
