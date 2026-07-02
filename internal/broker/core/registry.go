package core

import (
	"fmt"

	"github.com/maryzam/ai-crew-localdev/internal/broker/api"
	"github.com/maryzam/ai-crew-localdev/internal/broker/port"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/policy"
)

type providerRegistry struct {
	byURIProvider map[string]port.Provider
	credentials   map[string]port.CredentialProvider
	telemetry     map[string]port.TelemetryProvider
}

func newProviderRegistry(providers []port.Provider) (*providerRegistry, error) {
	r := &providerRegistry{
		byURIProvider: map[string]port.Provider{},
		credentials:   map[string]port.CredentialProvider{},
		telemetry:     map[string]port.TelemetryProvider{},
	}
	for _, p := range providers {
		if _, exists := r.byURIProvider[p.URIProvider()]; exists {
			return nil, fmt.Errorf("provider registration: duplicate URI provider %q", p.URIProvider())
		}
		credential, hasCredential := p.(port.CredentialProvider)
		telemetry, hasTelemetry := p.(port.TelemetryProvider)
		if !hasCredential && !hasTelemetry {
			return nil, fmt.Errorf("provider registration: provider %q has no broker capability", p.URIProvider())
		}
		if hasCredential {
			if _, exists := r.credentials[credential.Type()]; exists {
				return nil, fmt.Errorf("provider registration: duplicate credential_type %q", credential.Type())
			}
			r.credentials[credential.Type()] = credential
		}
		if hasTelemetry {
			r.telemetry[p.URIProvider()] = telemetry
		}
		r.byURIProvider[p.URIProvider()] = p
	}
	return r, nil
}

func (r *providerRegistry) credentialTypeFor(uriProvider string) (string, bool) {
	provider, ok := r.byURIProvider[uriProvider]
	if !ok {
		return "", false
	}
	credential, ok := provider.(port.CredentialProvider)
	if !ok {
		return "", false
	}
	return credential.Type(), true
}

func (r *providerRegistry) uriProviders() []string {
	names := make([]string, 0, len(r.byURIProvider))
	for name := range r.byURIProvider {
		names = append(names, name)
	}
	return names
}

func (r *providerRegistry) provider(credType string) (port.CredentialProvider, bool) {
	p, ok := r.credentials[credType]
	return p, ok
}

func (r *providerRegistry) telemetryProvider(uriProvider string) (port.TelemetryProvider, bool) {
	p, ok := r.telemetry[uriProvider]
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
	required, err := r.requiredProviders(agent, ap.Resources)
	if err != nil {
		return nil, err
	}
	if len(required) == 0 {
		return nil, nil
	}
	configs := make(map[string]any, len(required))
	for uriProvider, provider := range required {
		section, ok := ap.Providers[uriProvider]
		if !ok || len(section) == 0 || string(section) == "null" {
			return nil, fmt.Errorf("policy: agent %q declares %s resources but providers.%s is missing", agent, uriProvider, uriProvider)
		}
		cfg, err := provider.ParseConfig(agent, section)
		if err != nil {
			return nil, fmt.Errorf("policy: %w", err)
		}
		configs[uriProvider] = cfg
	}
	return configs, nil
}

func (r *providerRegistry) requiredProviders(agent string, resources []string) (map[string]port.Provider, error) {
	required := map[string]port.Provider{}
	for _, raw := range resources {
		uri, err := api.ParseResourceURI(raw)
		if err != nil {
			return nil, fmt.Errorf("policy: agent %q resource %q: %w", agent, raw, err)
		}
		provider, ok := r.byURIProvider[uri.Provider]
		if !ok {
			return nil, fmt.Errorf("policy: agent %q: no provider registered for %s resources", agent, uri.Provider)
		}
		if err := provider.ValidateResource(uri); err != nil {
			return nil, fmt.Errorf("policy: agent %q resource %q: %w", agent, raw, err)
		}
		required[uri.Provider] = provider
	}
	return required, nil
}
