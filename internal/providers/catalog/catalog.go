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
	return buildProviders(providerDeps{githubBaseURL: githubBaseURL, signer: signer}, func(constructor providerConstructor, deps providerDeps) port.Provider {
		return constructor.broker(identities, deps)
	})
}

func Validators(identities *identity.IdentitiesFile) ([]port.Provider, error) {
	return buildProviders(providerDeps{}, func(constructor providerConstructor, deps providerDeps) port.Provider {
		return constructor.validator(identities, deps)
	})
}

type providerDeps struct {
	githubBaseURL string
	signer        *github.Signer
}

type providerConstructor struct {
	broker    func(*identity.IdentitiesFile, providerDeps) port.Provider
	validator func(*identity.IdentitiesFile, providerDeps) port.Provider
}

var constructors = map[string]providerConstructor{
	"github": {
		broker: func(identities *identity.IdentitiesFile, deps providerDeps) port.Provider {
			return github.New(github.NewGitHubClient(deps.githubBaseURL), deps.signer, appIDResolver(identities))
		},
		validator: func(identities *identity.IdentitiesFile, _ providerDeps) port.Provider {
			return github.NewValidator(appIDResolver(identities))
		},
	},
	"langfuse": {
		broker: func(*identity.IdentitiesFile, providerDeps) port.Provider {
			return langfuse.New()
		},
		validator: func(*identity.IdentitiesFile, providerDeps) port.Provider {
			return langfuse.New()
		},
	},
}

func buildProviders(deps providerDeps, construct func(providerConstructor, providerDeps) port.Provider) ([]port.Provider, error) {
	var providers []port.Provider
	for _, provider := range capabilities.BrokerProviders() {
		constructor, ok := constructors[provider]
		if !ok {
			return nil, fmt.Errorf("no provider constructor registered for %q", provider)
		}
		providers = append(providers, construct(constructor, deps))
	}
	return providers, nil
}

func appIDResolver(identities *identity.IdentitiesFile) func(agent string) string {
	return func(agent string) string {
		if identities == nil {
			return ""
		}
		if a, ok := identities.Agents[agent]; ok {
			return a.AppID
		}
		return ""
	}
}
