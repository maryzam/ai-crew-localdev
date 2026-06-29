package cli

import (
	"github.com/maryzam/ai-crew-localdev/internal/broker"
	ghprov "github.com/maryzam/ai-crew-localdev/internal/broker/providers/github"
	lfprov "github.com/maryzam/ai-crew-localdev/internal/broker/providers/langfuse"
	"github.com/maryzam/ai-crew-localdev/internal/identity"
)

func validatorProviders(idents *identity.IdentitiesFile) []broker.CredentialProvider {
	return []broker.CredentialProvider{
		ghprov.NewValidator(identityAppIDResolver(idents)),
		lfprov.New(),
	}
}

func identityAppIDResolver(idents *identity.IdentitiesFile) func(string) string {
	if idents == nil {
		return func(string) string { return "" }
	}
	return func(agent string) string {
		if a, ok := idents.Agents[agent]; ok {
			return a.AppID
		}
		return ""
	}
}
