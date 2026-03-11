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

// GenerateDefault creates a PolicyFile with default values based on the given identities.
func GenerateDefault(identities *identity.IdentitiesFile) *PolicyFile {
	agents := make(map[string]AgentPolicy, len(identities.Agents))
	for name := range identities.Agents {
		agents[name] = AgentPolicy{
			AllowedRepos:       []string{},
			DefaultPermissions: DefaultPermissions(),
		}
	}

	return &PolicyFile{
		SchemaVersion:      schema.PolicySchemaV1,
		DefaultSessionTTL:  "8h",
		DefaultIdleTimeout: "1h",
		Agents:             agents,
	}
}
