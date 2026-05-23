package policy

import (
	"encoding/json"
	"fmt"

	"github.com/maryzam/ai-crew-localdev/internal/schema"
)

// LoadAutoDetect parses policy bytes, picking v1 or v2 based on
// schema_version. The returned PolicyFile is always in v1 shape: v2
// inputs are normalized into the legacy struct so broker code can
// continue to consume a single shape during the migration. The flag
// reports whether the source was v2. Validation errors from the matching
// validator are returned (warnings are dropped — callers that want them
// should call ValidateV2 / Validate directly).
func LoadAutoDetect(data []byte) (*PolicyFile, bool, error) {
	var probe struct {
		SchemaVersion string `json:"schema_version"`
	}
	if err := json.Unmarshal(data, &probe); err != nil {
		return nil, false, fmt.Errorf("parse schema_version: %w", err)
	}

	switch probe.SchemaVersion {
	case schema.PolicySchemaV2:
		v2, err := ParsePolicyV2(data)
		if err != nil {
			return nil, true, err
		}
		result := ValidateV2(v2)
		if result.Errors.HasErrors() {
			return nil, true, fmt.Errorf("validate v2 policy: %s", result.Errors.Error())
		}
		return normalizeV2(v2), true, nil
	case schema.PolicySchemaV1, "":
		v1, err := ParsePolicy(data)
		if err != nil {
			return nil, false, err
		}
		result := Validate(v1)
		if result.Errors.HasErrors() {
			return nil, false, fmt.Errorf("validate v1 policy: %s", result.Errors.Error())
		}
		return v1, false, nil
	default:
		return nil, false, fmt.Errorf("unknown schema_version %q (want %q or %q)",
			probe.SchemaVersion, schema.PolicySchemaV1, schema.PolicySchemaV2)
	}
}

// normalizeV2 projects a v2 policy down to the legacy v1 PolicyFile shape
// the broker currently consumes. github:repo:<owner/name> resources become
// AllowedRepos entries; github.installation_id and github.default_permissions
// move into the corresponding AgentPolicy fields. Resources whose provider
// is not "github" or kind is not "repo" are dropped — v2 validation has
// already rejected unknown providers, and non-github kinds are not yet
// supported by the broker's v1-shaped consumers. Stage 10 deletes this
// translation along with the v1 shape.
func normalizeV2(v2 *PolicyFileV2) *PolicyFile {
	out := &PolicyFile{
		SchemaVersion:      schema.PolicySchemaV1,
		DefaultSessionTTL:  v2.DefaultSessionTTL,
		DefaultIdleTimeout: v2.DefaultIdleTimeout,
		Agents:             make(map[string]AgentPolicy, len(v2.Agents)),
	}
	for name, a := range v2.Agents {
		ap := AgentPolicy{}
		for _, uri := range a.Resources {
			provider, kind, identifier, ok := splitResourceURI(uri)
			if !ok {
				continue
			}
			if provider == "github" && kind == "repo" {
				ap.AllowedRepos = append(ap.AllowedRepos, identifier)
			}
		}
		if a.GitHub != nil {
			id := a.GitHub.InstallationID
			if id != 0 {
				ap.InstallationID = &id
			}
			ap.DefaultPermissions = a.GitHub.DefaultPermissions
		}
		out.Agents[name] = ap
	}
	return out
}
