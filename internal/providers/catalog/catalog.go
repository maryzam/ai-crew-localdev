package catalog

import (
	"fmt"

	"github.com/maryzam/ai-crew-localdev/internal/broker/port"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/identity"
	"github.com/maryzam/ai-crew-localdev/internal/providers/capabilities"
	"github.com/maryzam/ai-crew-localdev/internal/providers/github"
	"github.com/maryzam/ai-crew-localdev/internal/providers/langfuse"
)

func Providers(identities *identity.IdentitiesFile, githubBaseURL string) ([]port.Provider, error) {
	signer, err := github.NewSigner(identities)
	if err != nil {
		return nil, fmt.Errorf("create signer: %w", err)
	}
	var providers []port.Provider
	for _, provider := range capabilities.BrokerProviders() {
		switch provider {
		case "github":
			providers = append(providers, github.New(github.NewGitHubClient(githubBaseURL), signer, appIDResolver(identities)))
		case "langfuse":
			providers = append(providers, langfuse.New())
		default:
			return nil, fmt.Errorf("no broker provider constructor registered for %q", provider)
		}
	}
	return providers, nil
}

func Validators(identities *identity.IdentitiesFile) ([]port.Provider, error) {
	var providers []port.Provider
	for _, provider := range capabilities.BrokerProviders() {
		switch provider {
		case "github":
			providers = append(providers, github.NewValidator(appIDResolver(identities)))
		case "langfuse":
			providers = append(providers, langfuse.New())
		default:
			return nil, fmt.Errorf("no provider validator registered for %q", provider)
		}
	}
	return providers, nil
}

func appIDResolver(identities *identity.IdentitiesFile) func(agent string) string {
	return func(agent string) string {
		if a, ok := identities.Agents[agent]; ok {
			return a.AppID
		}
		return ""
	}
}
