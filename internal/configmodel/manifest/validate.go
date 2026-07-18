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

	if f.SchemaVersion != schema.ManifestSchemaCurrent {
		result.Errors = append(result.Errors, schema.ValidationError{
			Field:   "schema_version",
			Message: fmt.Sprintf("must be %q, got %q", schema.ManifestSchemaCurrent, f.SchemaVersion),
		})
	}

	if len(f.Contracts) == 0 && f.Agents == nil && len(f.Resources) == 0 && len(f.Caches) == 0 && len(f.Services) == 0 && len(f.Ports) == 0 && len(f.Approvals) == 0 && len(f.RunModes) == 0 && len(f.ResourceBudgets) == 0 {
		result.Warnings = append(result.Warnings, Warning{
			Field:   "manifest",
			Message: "declares no contracts, agents, or operating model; the manifest has no effect",
		})
	}

	validateContracts(&result, f.Contracts)
	if f.Agents != nil {
		validateAgents(&result, f.Agents)
	}
	validateResources(&result, f.Resources)
	validateCaches(&result, f.Caches)
	validateServices(&result, f.Services)
	validatePorts(&result, f.Ports)
	validateApprovals(&result, f.Approvals)
	validateRunModes(&result, f.RunModes)
	validateResourceBudgets(&result, f.ResourceBudgets)

	return result
}

func validateContracts(result *ValidateResult, contracts []Contract) {
	seen := make(map[string]struct{}, len(contracts))
	for i, contract := range contracts {
		prefix := fmt.Sprintf("contracts[%d]", i)

		if validateName(result, prefix+".name", contract.Name) {
			if _, dup := seen[contract.Name]; dup {
				result.Errors = append(result.Errors, schema.ValidationError{
					Field:   prefix + ".name",
					Message: fmt.Sprintf("duplicate contract name %q", contract.Name),
				})
			} else {
				seen[contract.Name] = struct{}{}
			}
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

func validateResources(result *ValidateResult, resources []Resource) {
	seen := make(map[string]struct{}, len(resources))
	for i, resource := range resources {
		field := fmt.Sprintf("resources[%d].uri", i)
		validateResourceURI(result, field, resource.URI)
		if resource.URI == "" {
			continue
		}
		if _, dup := seen[resource.URI]; dup {
			result.Errors = append(result.Errors, schema.ValidationError{
				Field:   field,
				Message: fmt.Sprintf("duplicate resource %q", resource.URI),
			})
			continue
		}
		seen[resource.URI] = struct{}{}
	}
}

func validateCaches(result *ValidateResult, caches []Cache) {
	seenNames := make(map[string]struct{}, len(caches))
	seenTargets := make(map[string]struct{}, len(caches))
	for i, cache := range caches {
		prefix := fmt.Sprintf("caches[%d]", i)
		validateName(result, prefix+".name", cache.Name)
		if strings.TrimSpace(cache.Target) == "" {
			result.Errors = append(result.Errors, schema.ValidationError{Field: prefix + ".target", Message: "must not be empty or whitespace"})
		} else if cache.Target != strings.TrimSpace(cache.Target) {
			result.Errors = append(result.Errors, schema.ValidationError{Field: prefix + ".target", Message: "must not have leading or trailing whitespace"})
		} else if !strings.HasPrefix(cache.Target, "/") || strings.Contains(cache.Target, `\`) || strings.Contains(cache.Target, "\x00") {
			result.Errors = append(result.Errors, schema.ValidationError{Field: prefix + ".target", Message: "must be an absolute container path"})
		}
		if cache.Name != "" {
			if _, dup := seenNames[cache.Name]; dup {
				result.Errors = append(result.Errors, schema.ValidationError{Field: prefix + ".name", Message: fmt.Sprintf("duplicate cache %q", cache.Name)})
			}
			seenNames[cache.Name] = struct{}{}
		}
		if cache.Target != "" {
			if _, dup := seenTargets[cache.Target]; dup {
				result.Errors = append(result.Errors, schema.ValidationError{Field: prefix + ".target", Message: fmt.Sprintf("duplicate cache target %q", cache.Target)})
			}
			seenTargets[cache.Target] = struct{}{}
		}
	}
}

func validateServices(result *ValidateResult, services []Service) {
	seen := make(map[string]struct{}, len(services))
	for i, service := range services {
		field := fmt.Sprintf("services[%d].name", i)
		validateName(result, field, service.Name)
		if service.Name == "" {
			continue
		}
		if _, dup := seen[service.Name]; dup {
			result.Errors = append(result.Errors, schema.ValidationError{
				Field:   field,
				Message: fmt.Sprintf("duplicate service %q", service.Name),
			})
			continue
		}
		seen[service.Name] = struct{}{}
	}
}

func validatePorts(result *ValidateResult, ports []Port) {
	seen := make(map[int]struct{}, len(ports))
	for i, port := range ports {
		field := fmt.Sprintf("ports[%d].number", i)
		if port.Number < 1 || port.Number > 65535 {
			result.Errors = append(result.Errors, schema.ValidationError{
				Field:   field,
				Message: "must be between 1 and 65535",
			})
			continue
		}
		if _, dup := seen[port.Number]; dup {
			result.Errors = append(result.Errors, schema.ValidationError{
				Field:   field,
				Message: fmt.Sprintf("duplicate port %d", port.Number),
			})
			continue
		}
		seen[port.Number] = struct{}{}
	}
}

func validateApprovals(result *ValidateResult, approvals []Approval) {
	seen := make(map[string]struct{}, len(approvals))
	for i, approval := range approvals {
		prefix := fmt.Sprintf("approvals[%d]", i)
		switch approval.Point {
		case ApprovalRunStart, ApprovalBrokerEscalation:
		default:
			result.Errors = append(result.Errors, schema.ValidationError{
				Field:   prefix + ".point",
				Message: fmt.Sprintf("must be %q or %q", ApprovalRunStart, ApprovalBrokerEscalation),
			})
		}
		switch approval.Policy {
		case ApprovalOperatorInvocation, ApprovalUnsupportedFailClose:
		default:
			result.Errors = append(result.Errors, schema.ValidationError{
				Field:   prefix + ".policy",
				Message: fmt.Sprintf("must be %q or %q", ApprovalOperatorInvocation, ApprovalUnsupportedFailClose),
			})
		}
		if approval.Point == ApprovalRunStart && approval.Policy != ApprovalOperatorInvocation {
			result.Errors = append(result.Errors, schema.ValidationError{
				Field:   prefix + ".policy",
				Message: fmt.Sprintf("%s approvals must use %q", ApprovalRunStart, ApprovalOperatorInvocation),
			})
		}
		if approval.Point == ApprovalBrokerEscalation && approval.Policy != ApprovalUnsupportedFailClose {
			result.Errors = append(result.Errors, schema.ValidationError{
				Field:   prefix + ".policy",
				Message: fmt.Sprintf("%s approvals must use %q until broker escalation is implemented", ApprovalBrokerEscalation, ApprovalUnsupportedFailClose),
			})
		}
		key := approval.Point + "\x00" + approval.Policy
		if _, dup := seen[key]; dup {
			result.Errors = append(result.Errors, schema.ValidationError{
				Field:   prefix + ".point",
				Message: "duplicate approval declaration",
			})
			continue
		}
		seen[key] = struct{}{}
	}
}

func validateRunModes(result *ValidateResult, modes []string) {
	seen := make(map[string]struct{}, len(modes))
	for i, mode := range modes {
		field := fmt.Sprintf("run_modes[%d]", i)
		switch mode {
		case RunModeManagedRun, RunModeProjectDevcontainer:
		default:
			result.Errors = append(result.Errors, schema.ValidationError{
				Field:   field,
				Message: fmt.Sprintf("must be %q or %q", RunModeManagedRun, RunModeProjectDevcontainer),
			})
			continue
		}
		if _, dup := seen[mode]; dup {
			result.Errors = append(result.Errors, schema.ValidationError{
				Field:   field,
				Message: fmt.Sprintf("duplicate run mode %q", mode),
			})
			continue
		}
		seen[mode] = struct{}{}
	}
}

func validateResourceBudgets(result *ValidateResult, budgets []ResourceBudget) {
	seen := make(map[string]struct{}, len(budgets))
	for i, budget := range budgets {
		prefix := fmt.Sprintf("resource_budgets[%d]", i)
		validateName(result, prefix+".name", budget.Name)
		if budget.Name != "" {
			if _, dup := seen[budget.Name]; dup {
				result.Errors = append(result.Errors, schema.ValidationError{Field: prefix + ".name", Message: fmt.Sprintf("duplicate budget %q", budget.Name)})
			}
			seen[budget.Name] = struct{}{}
		}
		if budget.Metric != BudgetMetricTokens {
			result.Errors = append(result.Errors, schema.ValidationError{
				Field:   prefix + ".metric",
				Message: fmt.Sprintf("must be %q; unsupported resource metrics fail closed", BudgetMetricTokens),
			})
		}
		switch budget.MeasurementSource {
		case "", BudgetMeasurementNativeOTEL:
		default:
			result.Errors = append(result.Errors, schema.ValidationError{
				Field:   prefix + ".measurement_source",
				Message: fmt.Sprintf("must be %q", BudgetMeasurementNativeOTEL),
			})
		}
		switch budget.StopPolicy {
		case "", BudgetStopPolicyWarnOnly, BudgetStopPolicyStopRun:
		default:
			result.Errors = append(result.Errors, schema.ValidationError{
				Field:   prefix + ".stop_policy",
				Message: fmt.Sprintf("must be %q or %q", BudgetStopPolicyWarnOnly, BudgetStopPolicyStopRun),
			})
		}
		if budget.WarnAt < 0 {
			result.Errors = append(result.Errors, schema.ValidationError{Field: prefix + ".warn_at", Message: "must be zero or greater"})
		}
		if budget.StopAt < 0 {
			result.Errors = append(result.Errors, schema.ValidationError{Field: prefix + ".stop_at", Message: "must be zero or greater"})
		}
		if budget.WarnAt == 0 && budget.StopAt == 0 {
			result.Errors = append(result.Errors, schema.ValidationError{Field: prefix + ".warn_at", Message: "warn_at or stop_at must be greater than zero"})
		}
		if budget.WarnAt > 0 && budget.StopAt > 0 && budget.WarnAt > budget.StopAt {
			result.Errors = append(result.Errors, schema.ValidationError{Field: prefix + ".warn_at", Message: "must be less than or equal to stop_at"})
		}
		if budget.StopPolicy == BudgetStopPolicyStopRun && budget.StopAt == 0 {
			result.Errors = append(result.Errors, schema.ValidationError{Field: prefix + ".stop_at", Message: "must be greater than zero when stop_policy is stop_run"})
		}
		if budget.StopPolicy == BudgetStopPolicyWarnOnly && budget.StopAt > 0 {
			result.Errors = append(result.Errors, schema.ValidationError{Field: prefix + ".stop_at", Message: "must be zero when stop_policy is warn_only"})
		}
	}
}

func validateName(result *ValidateResult, field string, value string) bool {
	if strings.TrimSpace(value) == "" {
		result.Errors = append(result.Errors, schema.ValidationError{Field: field, Message: "must not be empty or whitespace"})
		return false
	}
	if value != strings.TrimSpace(value) {
		result.Errors = append(result.Errors, schema.ValidationError{Field: field, Message: "must not have leading or trailing whitespace"})
		return false
	}
	if len(value) > MaxContractNameLength {
		result.Errors = append(result.Errors, schema.ValidationError{Field: field, Message: fmt.Sprintf("must be at most %d characters", MaxContractNameLength)})
		return false
	}
	return true
}

func validateResourceURI(result *ValidateResult, field string, value string) {
	if strings.TrimSpace(value) == "" {
		result.Errors = append(result.Errors, schema.ValidationError{Field: field, Message: "must not be empty or whitespace"})
		return
	}
	if value != strings.TrimSpace(value) {
		result.Errors = append(result.Errors, schema.ValidationError{Field: field, Message: "must not have leading or trailing whitespace"})
		return
	}
	provider, rest, ok := strings.Cut(value, ":")
	if !ok || provider == "" {
		result.Errors = append(result.Errors, schema.ValidationError{Field: field, Message: "must use provider:kind:identifier format"})
		return
	}
	kind, identifier, ok := strings.Cut(rest, ":")
	if !ok || kind == "" || identifier == "" || strings.ContainsAny(provider+kind+identifier, " \t\r\n") {
		result.Errors = append(result.Errors, schema.ValidationError{Field: field, Message: "must use provider:kind:identifier format without whitespace"})
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
