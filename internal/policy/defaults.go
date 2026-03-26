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
// If an identity has an installation_id, it is copied into the generated policy.
func GenerateDefault(identities *identity.IdentitiesFile) *PolicyFile {
	agents := make(map[string]AgentPolicy, len(identities.Agents))
	for name, ident := range identities.Agents {
		ap := AgentPolicy{
			AllowedRepos:       []string{},
			DefaultPermissions: DefaultPermissions(),
		}
		if ident.InstallationID != nil {
			id := *ident.InstallationID
			ap.InstallationID = &id
		}
		agents[name] = ap
	}

	return &PolicyFile{
		SchemaVersion:      schema.PolicySchemaV1,
		DefaultSessionTTL:  "8h",
		DefaultIdleTimeout: "1h",
		Agents:             agents,
	}
}
