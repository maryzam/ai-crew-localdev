package broker

import (
	"errors"
	"fmt"
	"sync"

	"github.com/maryzam/ai-crew-localdev/internal/brokerapi"
	"github.com/maryzam/ai-crew-localdev/internal/policy"
)

var ErrUnknownCredentialType = errors.New("unknown credential type")

var ErrResourceNotAllowed = errors.New("resource not allowed")

type PolicyEnforcer struct {
	mu             sync.RWMutex
	policy         *policy.PolicyFile
	knownProviders map[string]struct{}
}

func NewPolicyEnforcer(p *policy.PolicyFile, knownURIProviders ...string) *PolicyEnforcer {
	known := make(map[string]struct{}, len(knownURIProviders))
	for _, name := range knownURIProviders {
		known[name] = struct{}{}
	}
	return &PolicyEnforcer{policy: p, knownProviders: known}
}

func (e *PolicyEnforcer) AuthorizeResource(agentName string, resource brokerapi.ResourceURI) error {
	e.mu.RLock()
	defer e.mu.RUnlock()

	agentPolicy, ok := e.policy.Agents[agentName]
	if !ok {
		return fmt.Errorf("%w: agent %q not in policy", ErrResourceNotAllowed, agentName)
	}

	if _, served := e.knownProviders[resource.Provider]; !served {
		return fmt.Errorf("%w: %s:%s", ErrUnknownCredentialType, resource.Provider, resource.Kind)
	}

	target := resource.String()
	for _, uri := range agentPolicy.Resources {
		if uri == target {
			return nil
		}
	}
	return fmt.Errorf("%w: resource %q for agent %q", ErrResourceNotAllowed, target, agentName)
}

func (e *PolicyEnforcer) Policy() *policy.PolicyFile {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.policy
}

func (e *PolicyEnforcer) ProviderSection(agentName, providerName string) ([]byte, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	agentPolicy, ok := e.policy.Agents[agentName]
	if !ok {
		return nil, false
	}
	section, ok := agentPolicy.Providers[providerName]
	if !ok || len(section) == 0 || string(section) == "null" {
		return nil, false
	}
	return section, true
}

func (e *PolicyEnforcer) SetPolicy(p *policy.PolicyFile) {
	e.mu.Lock()
	e.policy = p
	e.mu.Unlock()
}
