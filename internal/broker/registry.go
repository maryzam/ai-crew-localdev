package broker

import (
	"fmt"

	"github.com/maryzam/ai-crew-localdev/internal/brokerapi"
	"github.com/maryzam/ai-crew-localdev/internal/brokerport"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/policy"
)

type providerRegistry struct {
	byType        map[string]brokerport.CredentialProvider
	byURIProvider map[string]string
}

func newProviderRegistry(providers []brokerport.CredentialProvider) (*providerRegistry, error) {
	r := &providerRegistry{
		byType:        map[string]brokerport.CredentialProvider{},
		byURIProvider: map[string]string{},
	}
	for _, p := range providers {
		if _, exists := r.byType[p.Type()]; exists {
			return nil, fmt.Errorf("provider registration: duplicate credential_type %q", p.Type())
		}
		if existing, exists := r.byURIProvider[p.URIProvider()]; exists {
			return nil, fmt.Errorf("provider registration: URI provider %q already served by credential_type %q",
				p.URIProvider(), existing)
		}
		r.byType[p.Type()] = p
		r.byURIProvider[p.URIProvider()] = p.Type()
	}
	return r, nil
}

func (r *providerRegistry) credentialTypeFor(uriProvider string) (string, bool) {
	t, ok := r.byURIProvider[uriProvider]
	return t, ok
}

func (r *providerRegistry) provider(credType string) (brokerport.CredentialProvider, bool) {
	p, ok := r.byType[credType]
	return p, ok
}

func (r *providerRegistry) validateAndParseConfigs(p *policy.PolicyFile) (map[string]map[string]any, error) {
	configs := make(map[string]map[string]any, len(p.Agents))
	for agent, ap := range p.Agents {
		agentConfigs, err := r.parseAgent(agent, ap)
		if err != nil {
			return nil, err
		}
		if len(agentConfigs) > 0 {
			configs[agent] = agentConfigs
		}
	}
	return configs, nil
}

func (r *providerRegistry) parseAgent(agent string, ap policy.AgentPolicy) (map[string]any, error) {
	required, err := r.requiredCredentialTypes(agent, ap.Resources)
	if err != nil {
		return nil, err
	}
	if len(required) == 0 {
		return nil, nil
	}
	configs := make(map[string]any, len(required))
	for uriProvider, credType := range required {
		section, ok := ap.Providers[uriProvider]
		if !ok || len(section) == 0 || string(section) == "null" {
			return nil, fmt.Errorf("policy: agent %q declares %s resources but providers.%s is missing", agent, uriProvider, uriProvider)
		}
		cfg, err := r.byType[credType].ParseConfig(agent, section)
		if err != nil {
			return nil, fmt.Errorf("policy: %w", err)
		}
		configs[credType] = cfg
	}
	return configs, nil
}

func (r *providerRegistry) requiredCredentialTypes(agent string, resources []string) (map[string]string, error) {
	required := map[string]string{}
	for _, raw := range resources {
		uri, err := brokerapi.ParseResourceURI(raw)
		if err != nil {
			return nil, fmt.Errorf("policy: agent %q resource %q: %w", agent, raw, err)
		}
		credType, ok := r.byURIProvider[uri.Provider]
		if !ok {
			return nil, fmt.Errorf("policy: agent %q: no provider registered for %s resources", agent, uri.Provider)
		}
		if err := r.byType[credType].ValidateResource(uri); err != nil {
			return nil, fmt.Errorf("policy: agent %q resource %q: %w", agent, raw, err)
		}
		required[uri.Provider] = credType
	}
	return required, nil
}
