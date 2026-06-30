package cli

import (
	"context"
	"fmt"

	"github.com/maryzam/ai-crew-localdev/internal/identity"
	"github.com/maryzam/ai-crew-localdev/internal/policy"
	githubcontract "github.com/maryzam/ai-crew-localdev/internal/providers/github/contract"
)

type GitHubSetupClient interface {
	ListInstallations(context.Context, string) ([]githubcontract.Installation, error)
	MintInstallationToken(context.Context, string, int64, string, map[string]string) (*githubcontract.InstallationToken, error)
	ListInstallationRepos(context.Context, string) ([]githubcontract.Repository, error)
}

type JWTSigner interface {
	SignJWT(string) (string, error)
}

type ProviderServices struct {
	GitHubClient   GitHubSetupClient
	NewSigner      func(*identity.IdentitiesFile) (JWTSigner, error)
	ValidatePolicy func(*policy.PolicyFile, *identity.IdentitiesFile) error
}

var providerServices ProviderServices

func ConfigureProviderServices(services ProviderServices) {
	providerServices = services
}

func configuredProviderServices() (ProviderServices, error) {
	if providerServices.GitHubClient == nil || providerServices.NewSigner == nil || providerServices.ValidatePolicy == nil {
		return ProviderServices{}, fmt.Errorf("provider services are not configured")
	}
	return providerServices, nil
}

func validateConfiguredPolicy(policyFile *policy.PolicyFile, identities *identity.IdentitiesFile) error {
	services, err := configuredProviderServices()
	if err != nil {
		return err
	}
	return services.ValidatePolicy(policyFile, identities)
}
