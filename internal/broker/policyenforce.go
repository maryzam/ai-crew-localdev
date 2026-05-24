package broker

import (
	"errors"
	"fmt"
	"sync"

	"github.com/maryzam/ai-crew-localdev/internal/policy"
)

// ErrUnknownCredentialType is returned by AuthorizeResource when the
// resource's provider/kind is not recognized by any registered provider.
var ErrUnknownCredentialType = errors.New("unknown credential type")

// ErrResourceNotAllowed is returned by AuthorizeResource when the resource
// is not in the agent's allowed set (or the agent itself is not in policy).
var ErrResourceNotAllowed = errors.New("resource not allowed")

// PolicyEnforcer performs runtime authorization checks against the loaded policy.
type PolicyEnforcer struct {
	mu              sync.RWMutex
	policy          *policy.PolicyFile
	knownProviders  map[string]struct{}
}

// NewPolicyEnforcer creates an enforcer that recognizes the given URI provider
// names (e.g. "github") as valid resource owners. Resources whose provider is
// not in this set are rejected with ErrUnknownCredentialType.
func NewPolicyEnforcer(p *policy.PolicyFile, knownURIProviders ...string) *PolicyEnforcer {
	known := make(map[string]struct{}, len(knownURIProviders))
	for _, name := range knownURIProviders {
		known[name] = struct{}{}
	}
	return &PolicyEnforcer{policy: p, knownProviders: known}
}

// AuthorizeResource reports whether the agent is permitted to access the
// resource. Returns ErrUnknownCredentialType if no provider serves the URI's
// provider prefix.
func (e *PolicyEnforcer) AuthorizeResource(agentName string, resource ResourceURI) error {
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

// Policy returns the loaded policy document.
func (e *PolicyEnforcer) Policy() *policy.PolicyFile {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.policy
}

// ProviderSection returns the raw policy section for a (agent, providerName).
// Empty/missing sections return ok=false.
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

// SetPolicy atomically replaces the enforcer's policy. The caller is expected
// to have parsed and validated p first; the broker calls this under its own
// lock so the swap is atomic with the matching agent-config update.
func (e *PolicyEnforcer) SetPolicy(p *policy.PolicyFile) {
	e.mu.Lock()
	e.policy = p
	e.mu.Unlock()
}
