package policy

import (
	"encoding/json"
	"fmt"
	"regexp"
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

	if f.SchemaVersion != schema.PolicySchemaV1 {
		result.Errors = append(result.Errors, schema.ValidationError{
			Field:   "schema_version",
			Message: fmt.Sprintf("must be %q, got %q", schema.PolicySchemaV1, f.SchemaVersion),
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

		if agent.InstallationID == nil || *agent.InstallationID <= 0 {
			result.Warnings = append(result.Warnings, Warning{
				Field:   prefix + ".installation_id",
				Message: "missing or zero; the broker will reject token requests for this agent until installation_id is set",
			})
		}

		if len(agent.AllowedRepos) == 0 {
			result.Warnings = append(result.Warnings, Warning{
				Field:   prefix + ".allowed_repos",
				Message: "empty; the agent cannot access any repositories until repos are added",
			})
		}

		for _, repo := range agent.AllowedRepos {
			if !repoSlugPattern.MatchString(repo) {
				result.Errors = append(result.Errors, schema.ValidationError{
					Field:   prefix + ".allowed_repos",
					Message: fmt.Sprintf("invalid repo slug %q, must match owner/repo format", repo),
				})
			}
		}

		if len(agent.DefaultPermissions) == 0 {
			result.Errors = append(result.Errors, schema.ValidationError{
				Field:   prefix + ".default_permissions",
				Message: "must not be empty",
			})
		}

		for key, val := range agent.DefaultPermissions {
			if !validPermValues[val] {
				result.Errors = append(result.Errors, schema.ValidationError{
					Field:   prefix + ".default_permissions." + key,
					Message: fmt.Sprintf("invalid permission value %q, must be one of: read, write, admin", val),
				})
			}
			if !knownPermissionKeys[key] {
				result.Warnings = append(result.Warnings, Warning{
					Field:   prefix + ".default_permissions." + key,
					Message: fmt.Sprintf("unknown permission key %q", key),
				})
			}
		}
	}

	return result
}
