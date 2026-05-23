package policy

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/schema"
)

// ParsePolicyV2 parses raw JSON bytes into a PolicyFileV2.
func ParsePolicyV2(data []byte) (*PolicyFileV2, error) {
	var f PolicyFileV2
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("failed to parse v2 policy file: %w", err)
	}
	return &f, nil
}

// knownProviders is the set of per-agent provider sections recognized by
// the v2 schema. The map drives both validation and any future per-section
// dispatch in the broker.
var knownProviders = map[string]bool{
	"github": true,
}

// ValidateV2 checks a PolicyFileV2 for correctness. It returns errors and
// warnings using the same shape as Validate. A v1 schema_version produces
// a single error with a migration hint and validation stops.
func ValidateV2(f *PolicyFileV2) ValidateResult {
	var result ValidateResult

	if f.SchemaVersion == schema.PolicySchemaV1 {
		result.Errors = append(result.Errors, schema.ValidationError{
			Field:   "schema_version",
			Message: fmt.Sprintf("v1 schema %q is not accepted by v2 validator; migrate to schema_version %q (replace allowed_repos with resources: [\"github:repo:owner/name\"] and move installation_id + default_permissions under a github: section)", schema.PolicySchemaV1, schema.PolicySchemaV2),
		})
		return result
	}
	if f.SchemaVersion != schema.PolicySchemaV2 {
		result.Errors = append(result.Errors, schema.ValidationError{
			Field:   "schema_version",
			Message: fmt.Sprintf("must be %q, got %q", schema.PolicySchemaV2, f.SchemaVersion),
		})
	}

	if f.DefaultSessionTTL == "" {
		result.Errors = append(result.Errors, schema.ValidationError{
			Field:   "default_session_ttl",
			Message: "must not be empty",
		})
	} else if _, err := time.ParseDuration(f.DefaultSessionTTL); err != nil {
		result.Errors = append(result.Errors, schema.ValidationError{
			Field:   "default_session_ttl",
			Message: fmt.Sprintf("invalid duration: %v", err),
		})
	}

	if f.DefaultIdleTimeout == "" {
		result.Errors = append(result.Errors, schema.ValidationError{
			Field:   "default_idle_timeout",
			Message: "must not be empty",
		})
	} else if _, err := time.ParseDuration(f.DefaultIdleTimeout); err != nil {
		result.Errors = append(result.Errors, schema.ValidationError{
			Field:   "default_idle_timeout",
			Message: fmt.Sprintf("invalid duration: %v", err),
		})
	}

	if len(f.Agents) == 0 {
		result.Errors = append(result.Errors, schema.ValidationError{
			Field:   "agents",
			Message: "must contain at least one agent",
		})
		return result
	}

	for name, agent := range f.Agents {
		prefix := fmt.Sprintf("agents.%s", name)

		if len(agent.Resources) == 0 {
			result.Errors = append(result.Errors, schema.ValidationError{
				Field:   prefix + ".resources",
				Message: "must contain at least one resource URI",
			})
		}

		seenProviders := map[string]bool{}
		for i, uri := range agent.Resources {
			provider, kind, identifier, ok := splitResourceURI(uri)
			if !ok {
				result.Errors = append(result.Errors, schema.ValidationError{
					Field:   fmt.Sprintf("%s.resources[%d]", prefix, i),
					Message: fmt.Sprintf("invalid resource URI %q: expected provider:kind:identifier", uri),
				})
				continue
			}
			seenProviders[provider] = true
			if provider == "github" && kind == "repo" {
				if !repoSlugPattern.MatchString(identifier) {
					result.Errors = append(result.Errors, schema.ValidationError{
						Field:   fmt.Sprintf("%s.resources[%d]", prefix, i),
						Message: fmt.Sprintf("invalid github repo identifier %q, must match owner/repo format", identifier),
					})
				}
			}
		}

		if agent.GitHub != nil {
			gprefix := prefix + ".github"
			if agent.GitHub.InstallationID <= 0 {
				result.Warnings = append(result.Warnings, Warning{
					Field:   gprefix + ".installation_id",
					Message: "missing or zero; the broker will reject token requests for this agent until installation_id is set",
				})
			}
			if len(agent.GitHub.DefaultPermissions) == 0 {
				result.Errors = append(result.Errors, schema.ValidationError{
					Field:   gprefix + ".default_permissions",
					Message: "must not be empty",
				})
			}
			for key, val := range agent.GitHub.DefaultPermissions {
				if !validPermValues[val] {
					result.Errors = append(result.Errors, schema.ValidationError{
						Field:   gprefix + ".default_permissions." + key,
						Message: fmt.Sprintf("invalid permission value %q, must be one of: read, write, admin", val),
					})
				}
				if !knownPermissionKeys[key] {
					result.Warnings = append(result.Warnings, Warning{
						Field:   gprefix + ".default_permissions." + key,
						Message: fmt.Sprintf("unknown permission key %q", key),
					})
				}
			}
		}

		// Reject unknown providers referenced via resource URIs that have
		// no matching per-provider config section. Currently the only known
		// provider is "github"; an unknown URI provider is a schema error
		// because the broker has no way to mint that credential type.
		for p := range seenProviders {
			if !knownProviders[p] {
				result.Errors = append(result.Errors, schema.ValidationError{
					Field:   prefix + ".resources",
					Message: fmt.Sprintf("unknown provider %q in resource URI; known providers: github", p),
				})
			}
		}
	}

	return result
}

// splitResourceURI splits a provider:kind:identifier URI on the first two
// colons. Identifiers may contain further colons (e.g. AWS ARNs). Returns
// false if either of the first two colons is missing or any component is
// empty.
func splitResourceURI(s string) (provider, kind, identifier string, ok bool) {
	first := strings.IndexByte(s, ':')
	if first <= 0 {
		return "", "", "", false
	}
	rest := s[first+1:]
	second := strings.IndexByte(rest, ':')
	if second <= 0 {
		return "", "", "", false
	}
	id := rest[second+1:]
	if id == "" {
		return "", "", "", false
	}
	return s[:first], rest[:second], id, true
}
