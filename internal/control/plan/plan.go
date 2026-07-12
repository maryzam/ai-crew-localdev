package plan

import (
	"fmt"
	"strings"
	"unicode"
)

type BudgetMetric string

const (
	BudgetMetricTokens BudgetMetric = "tokens"
	BudgetMetricCost   BudgetMetric = "cost"
)

type BudgetStopPolicy string

const (
	BudgetStopPolicyWarnOnly BudgetStopPolicy = "warn_only"
	BudgetStopPolicyStopRun  BudgetStopPolicy = "stop_run"
)

type NetworkMode string

const (
	NetworkModeRestricted NetworkMode = "restricted"
	NetworkModeDisabled   NetworkMode = "disabled"
)

type ProjectedPathKind string

const (
	ProjectedPathDir  ProjectedPathKind = "dir"
	ProjectedPathFile ProjectedPathKind = "file"
)

type Draft struct {
	RunID      string
	TaskRef    string
	Repository Repository
	Agent      Agent
	Broker     BrokerSession
	Runtime    Runtime
	Env        Environment
	Intercept  Interception
	Home       Home
	Telemetry  Telemetry
	Budgets    []Budget
	Quality    Quality
	Retry      Retry
	Cleanup    Cleanup
}

type Repository struct {
	RootPath string
	Slug     string
	Remote   string
}

type Agent struct {
	Name            string
	Tool            string
	Type            string
	ConfiguredModel string
	CommandName     string
	Command         []string
	Model           ModelAttribution
}

type ModelAttribution struct {
	Provider   string
	Family     string
	Requested  string
	Resolution ModelResolution
}

type ModelResolution struct {
	Status        string
	Confidence    string
	PrimarySource string
	Sources       []string
	Conflict      bool
}

type BrokerSession struct {
	SocketPath   string
	AgentName    string
	HostRepoPath string
	Resources    []ProviderResource
}

type ProviderResource struct {
	URI        string
	Provider   string
	Kind       string
	Identifier string
}

type Runtime struct {
	WorkDir    string
	Mounts     []Mount
	Network    NetworkPolicy
	ExtraFiles []ExtraFile
}

type Mount struct {
	Source   string
	Target   string
	ReadOnly bool
}

type NetworkPolicy struct {
	Mode                 NetworkMode
	AllowedDestinations  []string
	FailClosedWhenAbsent bool
}

type ExtraFile struct {
	Name     string
	TargetFD int
}

type Environment struct {
	CredentialHelperPath string
	RealGhPath           string
	Variables            []EnvironmentVariable
}

type EnvironmentVariable struct {
	Name      string
	Value     string
	Sensitive bool
}

type Interception struct {
	Profiles []InterceptionProfile
	Wrappers []CommandWrapper
}

type InterceptionProfile struct {
	Provider         string
	Commands         []string
	ScrubEnv         []string
	ScrubEnvPrefixes []string
	FailClosedEnv    []EnvironmentVariable
}

type CommandWrapper struct {
	Provider string
	Command  string
	Path     string
}

type Home struct {
	SourceHome     string
	ProjectedPaths []ProjectedPath
}

type ProjectedPath struct {
	Name    string
	Kind    ProjectedPathKind
	Exclude []string
}

type Telemetry struct {
	LocalHistoryPath      string
	AuditLogPath          string
	NativeRelay           bool
	ObservabilitySinks    []ProviderResource
	EventsRetainedLocally bool
}

type Budget struct {
	Name       string
	Metric     BudgetMetric
	WarnAt     int64
	StopAt     int64
	StopPolicy BudgetStopPolicy
}

type Quality struct {
	Contracts []QualityContract
}

type QualityContract struct {
	Name            string
	Command         string
	WorkDir         string
	RetryAgent      bool
	TailLines       int
	EvidenceDir     string
	EvidenceMaxRuns int
}

type Retry struct {
	MaxAgentRetries int
}

type Cleanup struct {
	RevokeBrokerSession bool
	RemoveSessionInfo   bool
	CleanupHome         bool
}

type RunPlan struct {
	draft Draft
}

type ValidationError struct {
	Field   string
	Message string
}

func (e ValidationError) String() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

type ValidationErrors []ValidationError

func (ve ValidationErrors) Error() string {
	parts := make([]string, len(ve))
	for i, err := range ve {
		parts[i] = err.String()
	}
	return strings.Join(parts, "; ")
}

func (ve ValidationErrors) HasErrors() bool {
	return len(ve) > 0
}

func New(draft Draft) (RunPlan, error) {
	if errs := Validate(draft); errs.HasErrors() {
		return RunPlan{}, errs
	}
	return RunPlan{draft: cloneDraft(draft)}, nil
}

func (p RunPlan) Snapshot() Draft {
	return cloneDraft(p.draft)
}

func Validate(draft Draft) ValidationErrors {
	var errs ValidationErrors
	requireNonEmpty(&errs, "run_id", draft.RunID)
	validateRunID(&errs, draft.RunID)
	validateTaskRef(&errs, draft.TaskRef)
	requireNonEmpty(&errs, "repository.root_path", draft.Repository.RootPath)
	requireNonEmpty(&errs, "repository.slug", draft.Repository.Slug)
	requireNonEmpty(&errs, "agent.name", draft.Agent.Name)
	requireNonEmpty(&errs, "agent.type", draft.Agent.Type)
	requireNonEmpty(&errs, "agent.command_name", draft.Agent.CommandName)
	requireNonEmpty(&errs, "agent.model.resolution.status", draft.Agent.Model.Resolution.Status)
	requireNonEmpty(&errs, "agent.model.resolution.confidence", draft.Agent.Model.Resolution.Confidence)
	requireNonEmpty(&errs, "agent.model.resolution.primary_source", draft.Agent.Model.Resolution.PrimarySource)
	if len(draft.Agent.Command) == 0 {
		errs = append(errs, ValidationError{Field: "agent.command", Message: "must contain at least one argument"})
	} else {
		for i, arg := range draft.Agent.Command {
			requireNonEmpty(&errs, fmt.Sprintf("agent.command[%d]", i), arg)
		}
	}
	requireNonEmpty(&errs, "broker.socket_path", draft.Broker.SocketPath)
	requireNonEmpty(&errs, "broker.agent_name", draft.Broker.AgentName)
	requireNonEmpty(&errs, "broker.host_repo_path", draft.Broker.HostRepoPath)
	if draft.Agent.Name != "" && draft.Broker.AgentName != "" && draft.Agent.Name != draft.Broker.AgentName {
		errs = append(errs, ValidationError{Field: "broker.agent_name", Message: "must match agent.name"})
	}
	if draft.Repository.RootPath != "" && draft.Broker.HostRepoPath != "" && draft.Repository.RootPath != draft.Broker.HostRepoPath {
		errs = append(errs, ValidationError{Field: "broker.host_repo_path", Message: "must match repository.root_path"})
	}
	if len(draft.Broker.Resources) == 0 {
		errs = append(errs, ValidationError{Field: "broker.resources", Message: "must contain at least one planned resource"})
	}
	validateResources(&errs, "broker.resources", draft.Broker.Resources)
	validateRuntime(&errs, draft.Runtime)
	validateSecurity(&errs, draft)
	validateHome(&errs, draft.Home)
	validateTelemetry(&errs, draft.Telemetry)
	validateBudgets(&errs, draft.Budgets)
	validateQuality(&errs, draft.Quality)
	validateRetry(&errs, draft.Retry)
	return errs
}

func validateRunID(errs *ValidationErrors, value string) {
	if value == "" {
		return
	}
	if len(value) > 64 || !strings.HasPrefix(value, "run_") || len(value) == len("run_") {
		*errs = append(*errs, ValidationError{Field: "run_id", Message: "must use the run_ prefix and at most 64 printable ASCII characters"})
		return
	}
	if !isPrintableASCIIWithoutSpace(value) {
		*errs = append(*errs, ValidationError{Field: "run_id", Message: "must use the run_ prefix and at most 64 printable ASCII characters"})
	}
}

func validateTaskRef(errs *ValidationErrors, value string) {
	if value == "" {
		return
	}
	if len(value) > 200 {
		*errs = append(*errs, ValidationError{Field: "task_ref", Message: "must be at most 200 printable ASCII characters"})
		return
	}
	if !isPrintableASCIIWithoutSpace(value) {
		*errs = append(*errs, ValidationError{Field: "task_ref", Message: "must contain printable ASCII without whitespace"})
	}
}

func isPrintableASCIIWithoutSpace(value string) bool {
	for _, r := range value {
		if r > unicode.MaxASCII || unicode.IsControl(r) || unicode.IsSpace(r) {
			return false
		}
	}
	return true
}

func requireNonEmpty(errs *ValidationErrors, field string, value string) {
	if strings.TrimSpace(value) == "" {
		*errs = append(*errs, ValidationError{Field: field, Message: "must not be empty or whitespace"})
	}
}

func validateResources(errs *ValidationErrors, field string, resources []ProviderResource) {
	for i, resource := range resources {
		prefix := fmt.Sprintf("%s[%d]", field, i)
		requireNonEmpty(errs, prefix+".uri", resource.URI)
		requireNonEmpty(errs, prefix+".provider", resource.Provider)
		requireNonEmpty(errs, prefix+".kind", resource.Kind)
		requireNonEmpty(errs, prefix+".identifier", resource.Identifier)
		provider, kind, identifier, ok := splitResourceURI(resource.URI)
		if !ok {
			*errs = append(*errs, ValidationError{Field: prefix + ".uri", Message: "must use provider:kind:identifier format"})
			continue
		}
		if resource.Provider != "" && resource.Provider != provider {
			*errs = append(*errs, ValidationError{Field: prefix + ".provider", Message: "must match uri provider"})
		}
		if resource.Kind != "" && resource.Kind != kind {
			*errs = append(*errs, ValidationError{Field: prefix + ".kind", Message: "must match uri kind"})
		}
		if resource.Identifier != "" && resource.Identifier != identifier {
			*errs = append(*errs, ValidationError{Field: prefix + ".identifier", Message: "must match uri identifier"})
		}
	}
}

func splitResourceURI(uri string) (provider string, kind string, identifier string, ok bool) {
	provider, rest, ok := strings.Cut(uri, ":")
	if !ok || provider == "" {
		return "", "", "", false
	}
	kind, identifier, ok = strings.Cut(rest, ":")
	if !ok || kind == "" || identifier == "" {
		return "", "", "", false
	}
	return provider, kind, identifier, true
}

func validateRuntime(errs *ValidationErrors, runtime Runtime) {
	requireNonEmpty(errs, "runtime.work_dir", runtime.WorkDir)
	validateNetwork(errs, runtime.Network)
	for i, file := range runtime.ExtraFiles {
		if file.TargetFD < 3 {
			*errs = append(*errs, ValidationError{Field: fmt.Sprintf("runtime.extra_files[%d].target_fd", i), Message: "must be 3 or greater"})
		}
		requireNonEmpty(errs, fmt.Sprintf("runtime.extra_files[%d].name", i), file.Name)
	}
}

func validateNetwork(errs *ValidationErrors, network NetworkPolicy) {
	switch network.Mode {
	case NetworkModeRestricted, NetworkModeDisabled:
	default:
		*errs = append(*errs, ValidationError{Field: "runtime.network.mode", Message: fmt.Sprintf("must be %q or %q", NetworkModeRestricted, NetworkModeDisabled)})
	}
	if !network.FailClosedWhenAbsent {
		*errs = append(*errs, ValidationError{Field: "runtime.network.fail_closed_when_absent", Message: "must be true"})
	}
	if network.Mode == NetworkModeRestricted && len(network.AllowedDestinations) == 0 {
		*errs = append(*errs, ValidationError{Field: "runtime.network.allowed_destinations", Message: "must contain at least one destination when mode is restricted"})
	}
	for i, destination := range network.AllowedDestinations {
		requireNonEmpty(errs, fmt.Sprintf("runtime.network.allowed_destinations[%d]", i), destination)
	}
}

func validateSecurity(errs *ValidationErrors, draft Draft) {
	requireNonEmpty(errs, "env.credential_helper_path", draft.Env.CredentialHelperPath)
	if !hasExtraFile(draft.Runtime.ExtraFiles, "session_bind") {
		*errs = append(*errs, ValidationError{Field: "runtime.extra_files", Message: "must include session_bind for broker session authentication"})
	}
	if !draft.Cleanup.RevokeBrokerSession {
		*errs = append(*errs, ValidationError{Field: "cleanup.revoke_broker_session", Message: "must be true for managed runs"})
	}
	for i, variable := range draft.Env.Variables {
		requireNonEmpty(errs, fmt.Sprintf("env.variables[%d].name", i), variable.Name)
	}
	for i, profile := range draft.Intercept.Profiles {
		requireNonEmpty(errs, fmt.Sprintf("intercept.profiles[%d].provider", i), profile.Provider)
		for j, name := range profile.ScrubEnv {
			requireNonEmpty(errs, fmt.Sprintf("intercept.profiles[%d].scrub_env[%d]", i, j), name)
		}
		for j, prefix := range profile.ScrubEnvPrefixes {
			requireNonEmpty(errs, fmt.Sprintf("intercept.profiles[%d].scrub_env_prefixes[%d]", i, j), prefix)
		}
		for j, variable := range profile.FailClosedEnv {
			requireNonEmpty(errs, fmt.Sprintf("intercept.profiles[%d].fail_closed_env[%d].name", i, j), variable.Name)
		}
	}
	for i, wrapper := range draft.Intercept.Wrappers {
		requireNonEmpty(errs, fmt.Sprintf("intercept.wrappers[%d].provider", i), wrapper.Provider)
		requireNonEmpty(errs, fmt.Sprintf("intercept.wrappers[%d].command", i), wrapper.Command)
		requireNonEmpty(errs, fmt.Sprintf("intercept.wrappers[%d].path", i), wrapper.Path)
	}
}

func hasExtraFile(files []ExtraFile, name string) bool {
	for _, file := range files {
		if file.Name == name && file.TargetFD >= 3 {
			return true
		}
	}
	return false
}

func validateHome(errs *ValidationErrors, home Home) {
	requireNonEmpty(errs, "home.source_home", home.SourceHome)
	if len(home.ProjectedPaths) == 0 {
		*errs = append(*errs, ValidationError{Field: "home.projected_paths", Message: "must contain at least one path"})
	}
	seen := make(map[string]struct{}, len(home.ProjectedPaths))
	for i, path := range home.ProjectedPaths {
		prefix := fmt.Sprintf("home.projected_paths[%d]", i)
		validateProjectedPath(errs, prefix, path)
		if path.Name == "" {
			continue
		}
		if _, exists := seen[path.Name]; exists {
			*errs = append(*errs, ValidationError{Field: prefix + ".name", Message: "must be unique"})
			continue
		}
		seen[path.Name] = struct{}{}
	}
}

func validateProjectedPath(errs *ValidationErrors, field string, path ProjectedPath) {
	requireNonEmpty(errs, field+".name", path.Name)
	if !isSafeProjectedPathName(path.Name) {
		*errs = append(*errs, ValidationError{Field: field + ".name", Message: "must be one safe top-level path element"})
	}
	switch path.Kind {
	case ProjectedPathDir, ProjectedPathFile:
	default:
		*errs = append(*errs, ValidationError{Field: field + ".kind", Message: fmt.Sprintf("must be %q or %q", ProjectedPathDir, ProjectedPathFile)})
	}
	for i, exclude := range path.Exclude {
		excludeField := fmt.Sprintf("%s.exclude[%d]", field, i)
		requireNonEmpty(errs, excludeField, exclude)
		if !isSafeProjectedExclude(exclude) {
			*errs = append(*errs, ValidationError{Field: excludeField, Message: "must be one safe path element"})
		}
	}
}

func isSafeProjectedPathName(name string) bool {
	if name == "" || name == "." || name == ".." || strings.HasPrefix(name, "/") || strings.Contains(name, "/") || strings.Contains(name, `\`) {
		return false
	}
	_, blocked := blockedHomePathNames[name]
	return !blocked
}

func isSafeProjectedExclude(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.HasPrefix(name, "/") && !strings.Contains(name, "/") && !strings.Contains(name, `\`)
}

func validateTelemetry(errs *ValidationErrors, telemetry Telemetry) {
	if !telemetry.EventsRetainedLocally {
		*errs = append(*errs, ValidationError{Field: "telemetry.events_retained_locally", Message: "must be true before optional export"})
	}
	requireNonEmpty(errs, "telemetry.local_history_path", telemetry.LocalHistoryPath)
	validateResources(errs, "telemetry.observability_sinks", telemetry.ObservabilitySinks)
}

func validateBudgets(errs *ValidationErrors, budgets []Budget) {
	for i, budget := range budgets {
		prefix := fmt.Sprintf("budgets[%d]", i)
		requireNonEmpty(errs, prefix+".name", budget.Name)
		switch budget.Metric {
		case BudgetMetricTokens, BudgetMetricCost:
		default:
			*errs = append(*errs, ValidationError{Field: prefix + ".metric", Message: fmt.Sprintf("must be %q or %q", BudgetMetricTokens, BudgetMetricCost)})
		}
		switch budget.StopPolicy {
		case BudgetStopPolicyWarnOnly, BudgetStopPolicyStopRun:
		default:
			*errs = append(*errs, ValidationError{Field: prefix + ".stop_policy", Message: fmt.Sprintf("must be %q or %q", BudgetStopPolicyWarnOnly, BudgetStopPolicyStopRun)})
		}
		if budget.WarnAt < 0 {
			*errs = append(*errs, ValidationError{Field: prefix + ".warn_at", Message: "must be zero or greater"})
		}
		if budget.StopAt < 0 {
			*errs = append(*errs, ValidationError{Field: prefix + ".stop_at", Message: "must be zero or greater"})
		}
		if budget.StopPolicy == BudgetStopPolicyStopRun && budget.StopAt == 0 {
			*errs = append(*errs, ValidationError{Field: prefix + ".stop_at", Message: "must be greater than zero when stop_policy is stop_run"})
		}
		if budget.WarnAt > 0 && budget.StopAt > 0 && budget.WarnAt > budget.StopAt {
			*errs = append(*errs, ValidationError{Field: prefix + ".warn_at", Message: "must be less than or equal to stop_at"})
		}
	}
}

func validateQuality(errs *ValidationErrors, quality Quality) {
	for i, contract := range quality.Contracts {
		prefix := fmt.Sprintf("quality.contracts[%d]", i)
		requireNonEmpty(errs, prefix+".name", contract.Name)
		requireNonEmpty(errs, prefix+".command", contract.Command)
		requireNonEmpty(errs, prefix+".work_dir", contract.WorkDir)
		if contract.TailLines < 0 {
			*errs = append(*errs, ValidationError{Field: prefix + ".tail_lines", Message: "must be zero or greater"})
		}
		if contract.EvidenceMaxRuns < 0 {
			*errs = append(*errs, ValidationError{Field: prefix + ".evidence_max_runs", Message: "must be zero or greater"})
		}
	}
}

func validateRetry(errs *ValidationErrors, retry Retry) {
	if retry.MaxAgentRetries < 0 {
		*errs = append(*errs, ValidationError{Field: "retry.max_agent_retries", Message: "must be zero or greater"})
	}
}

func cloneDraft(draft Draft) Draft {
	draft.Agent.Command = append([]string(nil), draft.Agent.Command...)
	draft.Broker.Resources = cloneResources(draft.Broker.Resources)
	draft.Runtime.Mounts = append([]Mount(nil), draft.Runtime.Mounts...)
	draft.Runtime.Network.AllowedDestinations = append([]string(nil), draft.Runtime.Network.AllowedDestinations...)
	draft.Runtime.ExtraFiles = append([]ExtraFile(nil), draft.Runtime.ExtraFiles...)
	draft.Agent.Model.Resolution.Sources = append([]string(nil), draft.Agent.Model.Resolution.Sources...)
	draft.Env.Variables = append([]EnvironmentVariable(nil), draft.Env.Variables...)
	draft.Intercept.Profiles = cloneProfiles(draft.Intercept.Profiles)
	draft.Intercept.Wrappers = append([]CommandWrapper(nil), draft.Intercept.Wrappers...)
	draft.Home.ProjectedPaths = cloneProjectedPaths(draft.Home.ProjectedPaths)
	draft.Telemetry.ObservabilitySinks = cloneResources(draft.Telemetry.ObservabilitySinks)
	draft.Budgets = append([]Budget(nil), draft.Budgets...)
	draft.Quality.Contracts = append([]QualityContract(nil), draft.Quality.Contracts...)
	return draft
}

func cloneResources(resources []ProviderResource) []ProviderResource {
	return append([]ProviderResource(nil), resources...)
}

func cloneProfiles(profiles []InterceptionProfile) []InterceptionProfile {
	clone := make([]InterceptionProfile, len(profiles))
	for i, profile := range profiles {
		clone[i] = profile
		clone[i].Commands = append([]string(nil), profile.Commands...)
		clone[i].ScrubEnv = append([]string(nil), profile.ScrubEnv...)
		clone[i].ScrubEnvPrefixes = append([]string(nil), profile.ScrubEnvPrefixes...)
		clone[i].FailClosedEnv = append([]EnvironmentVariable(nil), profile.FailClosedEnv...)
	}
	return clone
}

func cloneProjectedPaths(paths []ProjectedPath) []ProjectedPath {
	clone := make([]ProjectedPath, len(paths))
	for i, path := range paths {
		clone[i] = path
		clone[i].Exclude = append([]string(nil), path.Exclude...)
	}
	return clone
}

var blockedHomePathNames = map[string]struct{}{
	".aws":             {},
	".azure":           {},
	".config":          {},
	".docker":          {},
	".git-credentials": {},
	".gitconfig":       {},
	".gnupg":           {},
	".kube":            {},
	".netrc":           {},
	".npmrc":           {},
	".pypirc":          {},
	".ssh":             {},
}
