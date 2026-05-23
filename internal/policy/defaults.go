package policy

import (
	"encoding/json"

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

// GenerateDefault creates a PolicyFile populated from the given identities.
// Each identity becomes an agent with an empty Resources list and a
// providers.github section seeded from the identity's installation_id.
func GenerateDefault(identities *identity.IdentitiesFile) *PolicyFile {
	agents := make(map[string]AgentPolicy, len(identities.Agents))
	for name, ident := range identities.Agents {
		gh := map[string]any{
			"default_permissions": DefaultPermissions(),
		}
		if ident.InstallationID != nil {
			gh["installation_id"] = *ident.InstallationID
		}
		section, _ := json.Marshal(gh)
		agents[name] = AgentPolicy{
			Resources: []string{},
			Providers: map[string]json.RawMessage{"github": section},
		}
	}

	return &PolicyFile{
		SchemaVersion:      schema.PolicySchemaCurrent,
		DefaultSessionTTL:  "8h",
		DefaultIdleTimeout: "1h",
		Agents:             agents,
	}
}
