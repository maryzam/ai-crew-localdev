package policy

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/schema"
)

// ParsePolicy parses raw JSON bytes into a PolicyFile.
func ParsePolicy(data []byte) (*PolicyFile, error) {
	var f PolicyFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("failed to parse policy file: %w", err)
	}
	return &f, nil
}

var (
	repoSlugPattern     = regexp.MustCompile(`^[a-zA-Z0-9._-]+/[a-zA-Z0-9._-]+$`)
	validPermValues     = map[string]bool{"read": true, "write": true, "admin": true}
	knownPermissionKeys = map[string]bool{
		"contents":        true,
		"pull_requests":   true,
		"metadata":        true,
		"issues":          true,
		"actions":         true,
		"checks":          true,
		"deployments":     true,
		"environments":    true,
		"packages":        true,
		"pages":           true,
		"security_events": true,
		"statuses":        true,
		"workflows":       true,
	}
	// knownProviders is the set of per-agent provider sections recognized
	// by the schema. Currently only GitHub is supported.
	knownProviders = map[string]bool{"github": true}
)

// Warning represents a non-fatal validation message.
type Warning struct {
	Field   string
	Message string
}

// ValidateResult contains both errors and warnings from validation.
type ValidateResult struct {
	Errors   schema.ValidationErrors
	Warnings []Warning
}

// Validate checks a PolicyFile for correctness and returns errors and warnings.
func Validate(f *PolicyFile) ValidateResult {
	var result ValidateResult

	if f.SchemaVersion != schema.PolicySchemaCurrent {
		result.Errors = append(result.Errors, schema.ValidationError{
			Field:   "schema_version",
			Message: fmt.Sprintf("must be %q, got %q", schema.PolicySchemaCurrent, f.SchemaVersion),
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
