package broker

import (
	"encoding/json"
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

// Authorize checks that the given agent is allowed to access the repo
// with the requested permissions.
func (e *PolicyEnforcer) Authorize(agentName, repo string, permissions map[string]string) error {
	e.mu.RLock()
	defer e.mu.RUnlock()

	agentPolicy, ok := e.policy.Agents[agentName]
	if !ok {
		return fmt.Errorf("agent %q not in policy", agentName)
	}

	repoAllowed := false
	for _, r := range agentPolicy.AllowedRepos {
		if r == repo {
			repoAllowed = true
			break
		}
	}
	if !repoAllowed {
		return fmt.Errorf("repo %q not allowed for agent %q", repo, agentName)
	}

	if len(permissions) > 0 {
		if err := ValidatePermissionSubset(permissions, agentPolicy.DefaultPermissions); err != nil {
			return fmt.Errorf("agent %q: %w", agentName, err)
		}
	}

	return nil
}

// AuthorizeResource checks that the given agent is permitted to access
// the given parsed resource. Only github:repo:<owner/name> is currently
// understood; any other provider/kind returns ErrUnknownCredentialType.
//
// AuthorizeResource is the credential-generic counterpart of Authorize.
// It does not check permission scopes — those are credential-type
// specific and validated by the provider during minting.
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

	for _, r := range agentPolicy.AllowedRepos {
		if r == resource.Identifier {
			return nil
		}
	}
	return fmt.Errorf("resource %q not allowed for agent %q", resource.String(), agentName)
}

// DefaultPermissions returns the default permission set for an agent.
func (e *PolicyEnforcer) DefaultPermissions(agentName string) (map[string]string, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	agentPolicy, ok := e.policy.Agents[agentName]
	if !ok {
		return nil, fmt.Errorf("agent %q not in policy", agentName)
	}
	return agentPolicy.DefaultPermissions, nil
}

// InstallationID returns the installation ID for the given agent, or an
// error if none is configured.
func (e *PolicyEnforcer) InstallationID(agentName string) (int64, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	agentPolicy, ok := e.policy.Agents[agentName]
	if !ok {
		return 0, fmt.Errorf("agent %q not in policy", agentName)
	}
	if agentPolicy.InstallationID == nil {
		return 0, fmt.Errorf("agent %q has no installation_id configured", agentName)
	}
	return *agentPolicy.InstallationID, nil
}

// Reload re-reads and validates the policy file, atomically replacing
// the enforcer's policy. Returns an error without modifying state if
// the new policy is invalid.
func (e *PolicyEnforcer) Reload(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("policy reload: read %s: %w", path, err)
	}

	var p policy.PolicyFile
	if err := json.Unmarshal(data, &p); err != nil {
		return fmt.Errorf("policy reload: parse: %w", err)
	}

	result := policy.Validate(&p)
	if result.Errors.HasErrors() {
		return fmt.Errorf("policy reload: validation failed: %s", result.Errors.Error())
	}

	e.mu.Lock()
	e.policy = &p
	e.mu.Unlock()

	return nil
}
