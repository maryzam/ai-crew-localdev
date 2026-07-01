package policy

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/configmodel/schema"
	"github.com/maryzam/ai-crew-localdev/internal/platform/securefile"
)

const maxPolicyBytes = 1 << 20

func Load(path string) (*PolicyFile, error) {
	data, err := securefile.ReadOwnerOnly(path, maxPolicyBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to read policy file: %w", err)
	}
	return ParsePolicy(data)
}

func ParsePolicy(data []byte) (*PolicyFile, error) {
	var f PolicyFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("failed to parse policy file: %w", err)
	}
	return &f, nil
}

type Warning struct {
	Field   string
	Message string
}

type ValidateResult struct {
	Errors   schema.ValidationErrors
	Warnings []Warning
}

func Validate(f *PolicyFile) ValidateResult {
	var result ValidateResult

	if f.SchemaVersion != schema.PolicySchemaCurrent {
		result.Errors = append(result.Errors, schema.ValidationError{
			Field:   "schema_version",
			Message: fmt.Sprintf("must be %q, got %q", schema.PolicySchemaCurrent, f.SchemaVersion),
		})
	}

	validateDuration(&result, "default_session_ttl", f.DefaultSessionTTL)
	validateDuration(&result, "default_idle_timeout", f.DefaultIdleTimeout)

	if len(f.Agents) == 0 {
		result.Errors = append(result.Errors, schema.ValidationError{
			Field:   "agents",
			Message: "must contain at least one agent",
		})
		return result
	}

	for name, agent := range f.Agents {
		validateAgent(&result, name, agent)
	}

	return result
}

func validateDuration(result *ValidateResult, field, value string) {
	if value == "" {
		result.Errors = append(result.Errors, schema.ValidationError{Field: field, Message: "must not be empty"})
		return
	}
	if _, err := time.ParseDuration(value); err != nil {
		result.Errors = append(result.Errors, schema.ValidationError{
			Field:   field,
			Message: fmt.Sprintf("invalid duration: %v", err),
		})
	}
}

func validateAgent(result *ValidateResult, name string, agent AgentPolicy) {
	prefix := "agents." + name

	if len(agent.Resources) == 0 {
		result.Errors = append(result.Errors, schema.ValidationError{
			Field:   prefix + ".resources",
			Message: "must contain at least one resource URI",
		})
	}

	required := map[string]bool{}
	for i, uri := range agent.Resources {
		provider, _, _, ok := splitResourceURI(uri)
		if !ok {
			result.Errors = append(result.Errors, schema.ValidationError{
				Field:   fmt.Sprintf("%s.resources[%d]", prefix, i),
				Message: fmt.Sprintf("invalid resource URI %q: expected provider:kind:identifier", uri),
			})
			continue
		}
		required[provider] = true
	}

	for provider := range required {
		section, present := agent.Providers[provider]
		if !present || len(section) == 0 || string(section) == "null" {
			result.Errors = append(result.Errors, schema.ValidationError{
				Field:   prefix + ".providers." + provider,
				Message: fmt.Sprintf("agent declares %s resources but providers.%s is missing", provider, provider),
			})
		}
	}
}

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
