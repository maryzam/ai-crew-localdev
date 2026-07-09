package manifest

import (
	"fmt"
	"slices"
	"strings"

	"github.com/maryzam/ai-crew-localdev/internal/configmodel/schema"
)

const MaxContractNameLength = 64

type Warning struct {
	Field   string
	Message string
}

type ValidateResult struct {
	Errors   schema.ValidationErrors
	Warnings []Warning
}

func Validate(f *File) ValidateResult {
	var result ValidateResult

	if f.SchemaVersion != schema.ManifestSchemaV1 {
		result.Errors = append(result.Errors, schema.ValidationError{
			Field:   "schema_version",
			Message: fmt.Sprintf("must be %q, got %q", schema.ManifestSchemaV1, f.SchemaVersion),
		})
	}

	if len(f.Contracts) == 0 && f.Agents == nil {
		result.Warnings = append(result.Warnings, Warning{
			Field:   "manifest",
			Message: "declares no contracts and no agents; the manifest has no effect",
		})
	}

	validateContracts(&result, f.Contracts)
	if f.Agents != nil {
		validateAgents(&result, f.Agents)
	}

	return result
}

func validateContracts(result *ValidateResult, contracts []Contract) {
	seen := make(map[string]struct{}, len(contracts))
	for i, contract := range contracts {
		prefix := fmt.Sprintf("contracts[%d]", i)

		if strings.TrimSpace(contract.Name) == "" {
			result.Errors = append(result.Errors, schema.ValidationError{
				Field:   prefix + ".name",
				Message: "must not be empty or whitespace",
			})
		} else if contract.Name != strings.TrimSpace(contract.Name) {
			result.Errors = append(result.Errors, schema.ValidationError{
				Field:   prefix + ".name",
				Message: "must not have leading or trailing whitespace",
			})
		} else if len(contract.Name) > MaxContractNameLength {
			result.Errors = append(result.Errors, schema.ValidationError{
				Field:   prefix + ".name",
				Message: fmt.Sprintf("must be at most %d characters", MaxContractNameLength),
			})
		} else if _, dup := seen[contract.Name]; dup {
			result.Errors = append(result.Errors, schema.ValidationError{
				Field:   prefix + ".name",
				Message: fmt.Sprintf("duplicate contract name %q", contract.Name),
			})
		} else {
			seen[contract.Name] = struct{}{}
		}

		if strings.TrimSpace(contract.Command) == "" {
			result.Errors = append(result.Errors, schema.ValidationError{
				Field:   prefix + ".command",
				Message: "must not be empty or whitespace; a blank command would pass as a no-op check",
			})
		}

		switch contract.Retry {
		case "", RetryAgent, RetryNever:
		default:
			result.Errors = append(result.Errors, schema.ValidationError{
				Field:   prefix + ".retry",
				Message: fmt.Sprintf("must be %q or %q, got %q", RetryAgent, RetryNever, contract.Retry),
			})
		}
	}
}

func validateAgents(result *ValidateResult, agents *Agents) {
	seen := make(map[string]struct{}, len(agents.Allowed))
	for i, name := range agents.Allowed {
		field := fmt.Sprintf("agents.allowed[%d]", i)
		if strings.TrimSpace(name) == "" {
			result.Errors = append(result.Errors, schema.ValidationError{
				Field:   field,
				Message: "must not be empty or whitespace",
			})
			continue
		}
		if name != strings.TrimSpace(name) {
			result.Errors = append(result.Errors, schema.ValidationError{
				Field:   field,
				Message: "must not have leading or trailing whitespace",
			})
			continue
		}
		if _, dup := seen[name]; dup {
			result.Errors = append(result.Errors, schema.ValidationError{
				Field:   field,
				Message: fmt.Sprintf("duplicate agent %q", name),
			})
			continue
		}
		seen[name] = struct{}{}
	}

	if len(agents.Allowed) == 0 && len(agents.Defaults) > 0 {
		result.Errors = append(result.Errors, schema.ValidationError{
			Field:   "agents.allowed",
			Message: "must list allowed agents when agents.defaults is declared",
		})
		return
	}

	for _, name := range sortedKeys(agents.Defaults) {
		if _, allowed := seen[name]; !allowed {
			result.Errors = append(result.Errors, schema.ValidationError{
				Field:   "agents.defaults." + name,
				Message: fmt.Sprintf("agent %q is not in agents.allowed", name),
			})
			continue
		}
		if strings.TrimSpace(agents.Defaults[name].Model) == "" {
			result.Errors = append(result.Errors, schema.ValidationError{
				Field:   "agents.defaults." + name + ".model",
				Message: "must not be empty or whitespace; model is the only default field, so a blank default declares nothing",
			})
		}
	}
}

func sortedKeys(defaults map[string]AgentDefaults) []string {
	keys := make([]string, 0, len(defaults))
	for key := range defaults {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	return keys
}
