package broker

import (
	"errors"
	"fmt"
	"os"
	"sync"

	"github.com/maryzam/ai-crew-localdev/internal/policy"
)

// ErrUnknownCredentialType is returned by AuthorizeResource when the
// resource's provider/kind is not recognized by the broker. Callers can
// translate this into ErrCodeUnknownCredType on the wire.
var ErrUnknownCredentialType = errors.New("unknown credential type")

// PolicyEnforcer performs runtime authorization checks against the loaded policy.
type PolicyEnforcer struct {
	mu     sync.RWMutex
	policy *policy.PolicyFile
}

// NewPolicyEnforcer creates an enforcer from a loaded policy file.
func NewPolicyEnforcer(p *policy.PolicyFile) *PolicyEnforcer {
	return &PolicyEnforcer{policy: p}
}

// AuthorizeResource checks that the given agent is permitted to access
// the given parsed resource. Only github:repo:<owner/name> is currently
// understood; any other provider/kind returns ErrUnknownCredentialType.
func (e *PolicyEnforcer) AuthorizeResource(agentName string, resource ResourceURI) error {
	e.mu.RLock()
	defer e.mu.RUnlock()

	agentPolicy, ok := e.policy.Agents[agentName]
	if !ok {
		return fmt.Errorf("agent %q not in policy", agentName)
	}

	if resource.Provider != "github" || resource.Kind != "repo" {
		return fmt.Errorf("%w: %s:%s", ErrUnknownCredentialType, resource.Provider, resource.Kind)
	}

	target := resource.String()
	for _, uri := range agentPolicy.Resources {
		if uri == target {
			return nil
		}
	}
	return fmt.Errorf("resource %q not allowed for agent %q", target, agentName)
}

// GitHubConfig returns the per-agent GitHub provider configuration, or
// an error if the agent is unknown or has no github: section.
func (e *PolicyEnforcer) GitHubConfig(agentName string) (*policy.GitHubAgentConfig, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	agentPolicy, ok := e.policy.Agents[agentName]
	if !ok {
		return nil, fmt.Errorf("agent %q not in policy", agentName)
	}
	if agentPolicy.GitHub == nil {
		return nil, fmt.Errorf("agent %q has no github configuration", agentName)
	}
	return agentPolicy.GitHub, nil
}

// Reload re-reads and validates the policy file, atomically replacing
// the enforcer's policy. Returns an error without modifying state if
// the new policy is invalid.
func (e *PolicyEnforcer) Reload(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("policy reload: read %s: %w", path, err)
	}

	p, err := policy.ParsePolicy(data)
	if err != nil {
		return fmt.Errorf("policy reload: %w", err)
	}
	result := policy.Validate(p)
	if result.Errors.HasErrors() {
		return fmt.Errorf("policy reload: validation failed: %s", result.Errors.Error())
	}

	e.mu.Lock()
	e.policy = p
	e.mu.Unlock()

	return nil
}
