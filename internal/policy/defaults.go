package policy

import (
	"github.com/maryzam/ai-crew-localdev/internal/identity"
	"github.com/maryzam/ai-crew-localdev/internal/schema"
)

// DefaultPermissions returns the standard default permission set for agents.
func DefaultPermissions() map[string]string {
	return map[string]string{
		"contents":      "write",
		"pull_requests": "write",
		"metadata":      "read",
	}
}

// GenerateDefault creates a PolicyFile with default values based on the
// given identities. Each identity becomes an agent with an empty
// Resources list and a github: section populated from the identity's
// installation_id (if any) and the default permission set.
func GenerateDefault(identities *identity.IdentitiesFile) *PolicyFile {
	agents := make(map[string]AgentPolicy, len(identities.Agents))
	for name, ident := range identities.Agents {
		gh := &GitHubAgentConfig{
			DefaultPermissions: DefaultPermissions(),
		}
		if ident.InstallationID != nil {
			gh.InstallationID = *ident.InstallationID
		}
		agents[name] = AgentPolicy{
			Resources: []string{},
			GitHub:    gh,
		}
	}

	return &PolicyFile{
		SchemaVersion:      schema.PolicySchemaCurrent,
		DefaultSessionTTL:  "8h",
		DefaultIdleTimeout: "1h",
		Agents:             agents,
	}
}
