package control

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	agentcaps "github.com/maryzam/ai-crew-localdev/internal/agents/capabilities"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/manifest"
	"github.com/maryzam/ai-crew-localdev/internal/control/plan"
	"github.com/maryzam/ai-crew-localdev/internal/interception"
	"github.com/maryzam/ai-crew-localdev/internal/platform/binresolve"
	"github.com/maryzam/ai-crew-localdev/internal/platform/correlation"
	"github.com/maryzam/ai-crew-localdev/internal/platform/paths"
	"github.com/maryzam/ai-crew-localdev/internal/platform/telemetry"
	"github.com/maryzam/ai-crew-localdev/internal/providers/capabilities"
)

type RunRequest struct {
	AgentName                string
	TaskRef                  string
	RepoPath                 string
	BrokerSocketPathOverride string
	CredentialHelperPath     string
	GhWrapperPath            string
	VerifyCommand            string
	MaxRetries               int
	TokenWarnAt              int64
	TokenStopAt              int64
	IsolateHome              bool
	AgentCommand             []string
	ObservabilityResource    string
}

type PlannedRun struct {
	Plan plan.RunPlan
}

type VerifyContract struct {
	Name          string
	Command       string
	FailurePolicy plan.QualityFailurePolicy
}

type Planner struct {
	errOut io.Writer
}

type projectManifestInfo struct {
	file *manifest.File
	path string
	root string
}

func NewPlanner(errOut io.Writer) Planner {
	if errOut == nil {
		errOut = io.Discard
	}
	return Planner{errOut: errOut}
}

func (planner Planner) PlanRun(request RunRequest) (PlannedRun, error) {
	if len(request.AgentCommand) == 0 {
		return PlannedRun{}, fmt.Errorf("no agent command specified; use -- to separate agent command from flags")
	}
	if err := validateMaxRetries(request.MaxRetries); err != nil {
		return PlannedRun{}, err
	}
	if err := validateTokenBudgetRequest(request.TokenWarnAt, request.TokenStopAt); err != nil {
		return PlannedRun{}, err
	}
	info, err := loadProjectManifest(planner.errOut, request.RepoPath)
	if err != nil {
		return PlannedRun{}, err
	}
	if err := info.enforceAgentAllowed(request.AgentName); err != nil {
		return PlannedRun{}, err
	}
	agentTool := agentcaps.DefaultToolForAgent(request.AgentName)
	if err := info.enforceAgentTool(request.AgentName, request.AgentCommand, agentTool); err != nil {
		return PlannedRun{}, err
	}
	if err := info.enforceRunMode(manifest.RunModeManagedRun); err != nil {
		return PlannedRun{}, err
	}
	if err := info.enforceApprovals(); err != nil {
		return PlannedRun{}, err
	}
	if os.Getenv(paths.EnvContainer) != "1" {
		return PlannedRun{}, fmt.Errorf("managed runs are devcontainer-only; start the devcontainer with ai-agent up and run ai-agent run inside it")
	}
	if err := correlation.ValidateTaskRef(request.TaskRef); err != nil {
		return PlannedRun{}, fmt.Errorf("invalid task reference: %w", err)
	}
	repo, err := ResolveRepository(request.RepoPath)
	if err != nil {
		return PlannedRun{}, fmt.Errorf("resolve repo: %w", err)
	}
	if repo.SSH {
		return PlannedRun{}, fmt.Errorf("repository %s uses an SSH remote; managed sessions require HTTPS remotes\nHint: git remote set-url origin https://github.com/%s.git", repo.RootPath, repo.Slug)
	}
	contracts, contractsDir := info.contracts(planner.errOut, request.VerifyCommand)
	configuredModel := ""
	if manifestModel := info.modelDefault(request.AgentName); manifestModel != "" {
		configuredModel = manifestModel
		_, _ = fmt.Fprintf(planner.errOut, "model: run attribution uses project manifest default %q for agent %s\n", manifestModel, request.AgentName)
	}
	agentAttribution, modelAttribution := agentcaps.ResolveAttribution(request.AgentName, configuredModel, request.AgentCommand)
	projectManifest := info.manifest()
	budgets, err := plannedBudgets(request, projectManifest.ResourceBudgetsCopy())
	if err != nil {
		return PlannedRun{}, err
	}
	socketPath, err := resolveBrokerSocketPath(request.BrokerSocketPathOverride)
	if err != nil {
		return PlannedRun{}, err
	}
	credentialHelper, err := resolveCredentialHelper(request.CredentialHelperPath)
	if err != nil {
		return PlannedRun{}, err
	}
	ghWrapper := request.GhWrapperPath
	if ghWrapper == "" {
		ghWrapper, _ = resolveOptionalBinary("ai-agent-gh")
	}
	realGhPath := ""
	if ghWrapper != "" {
		realGhPath = resolveRealGhPath(ghWrapper)
	}
	runID, err := telemetry.NewRunID()
	if err != nil {
		return PlannedRun{}, err
	}
	resources, observabilitySinks, err := plannedResources(repo.Slug, request.ObservabilityResource, projectManifest.ResourceURIs())
	if err != nil {
		return PlannedRun{}, err
	}
	qualityContracts := plannedQualityContracts(request, repo.RootPath, contracts, contractsDir)
	draft := plan.Draft{
		RunID:   runID,
		TaskRef: request.TaskRef,
		Repository: plan.Repository{
			RootPath: repo.RootPath,
			Slug:     repo.Slug,
			Remote:   repo.Remote,
		},
		Agent: plan.Agent{
			Name:            request.AgentName,
			Tool:            agentTool,
			Type:            agentAttribution.Type,
			ConfiguredModel: configuredModel,
			CommandName:     agentAttribution.Command,
			Command:         request.AgentCommand,
			Model:           plannedModelAttribution(modelAttribution),
		},
		Broker: plan.BrokerSession{
			SocketPath:   socketPath,
			AgentName:    request.AgentName,
			HostRepoPath: repo.RootPath,
			Resources:    resources,
		},
		Runtime: plan.Runtime{
			WorkDir: repo.RootPath,
			Network: plan.NetworkPolicy{
				Mode:                 plan.NetworkModeRestricted,
				AllowedDestinations:  []string{"github.com"},
				FailClosedWhenAbsent: true,
			},
			ExtraFiles: []plan.ExtraFile{{Name: "session_bind", TargetFD: 3}},
		},
		Env: plan.Environment{
			CredentialHelperPath: credentialHelper,
			RealGhPath:           realGhPath,
		},
		Intercept: plan.Interception{
			Profiles: plannedInterceptionProfiles(interception.Session{Repo: repo.Slug, CredentialHelperPath: credentialHelper}),
			Wrappers: plannedCommandWrappers(ghWrapper),
		},
		Home: plan.Home{
			SourceHome:     homeDir(),
			ProjectedPaths: projectedHomePaths(),
		},
		Telemetry: plan.Telemetry{
			LocalHistoryPath:      paths.RunTelemetryLogPath(),
			AuditLogPath:          paths.AuditLogPath(),
			NativeRelay:           true,
			ObservabilitySinks:    observabilitySinks,
			EventsRetainedLocally: true,
		},
		Budgets: budgets,
		Quality: plan.Quality{Contracts: qualityContracts},
		Retry:   plan.Retry{MaxAgentRetries: request.MaxRetries},
		Cleanup: plan.Cleanup{
			RevokeBrokerSession: true,
			RemoveSessionInfo:   true,
			CleanupHome:         request.IsolateHome,
		},
	}
	runPlan, err := plan.New(draft)
	if err != nil {
		return PlannedRun{}, err
	}
	return PlannedRun{Plan: runPlan}, nil
}

func plannedModelAttribution(model agentcaps.ModelAttribution) plan.ModelAttribution {
	return plan.ModelAttribution{
		Provider:  model.Provider,
		Family:    model.Family,
		Requested: model.Requested,
		Resolution: plan.ModelResolution{
			Status:        model.Resolution.Status,
			Confidence:    model.Resolution.Confidence,
			PrimarySource: model.Resolution.PrimarySource,
			Sources:       append([]string(nil), model.Resolution.Sources...),
			Conflict:      model.Resolution.Conflict,
		},
	}
}

func loadProjectManifest(errOut io.Writer, repoPath string) (*projectManifestInfo, error) {
	root := repoWorktreeRoot(repoPath)
	manifestPath, found, err := manifest.Find(root)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}
	file, err := manifest.Load(manifestPath)
	if err != nil {
		return nil, err
	}
	result := manifest.Validate(file)
	if result.Errors.HasErrors() {
		return nil, fmt.Errorf("invalid project manifest %s: %s", manifestPath, result.Errors.Error())
	}
	for _, warning := range result.Warnings {
		_, _ = fmt.Fprintf(errOut, "manifest: warning: %s: %s\n", warning.Field, warning.Message)
	}
	return &projectManifestInfo{file: file, path: manifestPath, root: root}, nil
}

func (info *projectManifestInfo) enforceAgentAllowed(agentName string) error {
	if info == nil {
		return nil
	}
	if !info.file.AllowsAgent(agentName) {
		return fmt.Errorf("agent %q is not allowed by the project manifest %s (allowed: %s)", agentName, info.path, info.file.AllowedAgentsText())
	}
	return nil
}

func (info *projectManifestInfo) enforceAgentTool(agentName string, command []string, tool string) error {
	if info == nil || info.file.Agents == nil || len(info.file.Agents.Allowed) == 0 {
		return nil
	}
	if tool == "" {
		return fmt.Errorf("agent %q is allowed by the project manifest %s but no compiled agent capability is configured", agentName, info.path)
	}
	if len(command) == 0 || !agentCommandMatchesTool(command[0], tool) {
		actual := ""
		if len(command) > 0 {
			actual = filepath.Base(strings.TrimSpace(command[0]))
		}
		return fmt.Errorf("agent %q is allowed by the project manifest %s but command %q does not match configured tool %q", agentName, info.path, actual, tool)
	}
	return nil
}

func (info *projectManifestInfo) modelDefault(agentName string) string {
	if info == nil || info.file.Agents == nil {
		return ""
	}
	return strings.TrimSpace(info.file.Agents.Defaults[agentName].Model)
}

func (info *projectManifestInfo) contracts(errOut io.Writer, verifyCmd string) ([]VerifyContract, string) {
	if info == nil || len(info.file.Contracts) == 0 {
		return nil, ""
	}
	if verifyCmd != "" {
		_, _ = fmt.Fprintf(errOut, "verify: --verify-cmd overrides %d project contract(s) from %s\n", len(info.file.Contracts), info.path)
		return nil, ""
	}
	contracts := make([]VerifyContract, 0, len(info.file.Contracts))
	for _, contract := range info.file.Contracts {
		contracts = append(contracts, VerifyContract{
			Name:          contract.Name,
			Command:       contract.Command,
			FailurePolicy: qualityFailurePolicy(contract.Retry),
		})
	}
	_, _ = fmt.Fprintf(errOut, "verify: %d project contract(s) declared in %s\n", len(contracts), info.path)
	return contracts, info.root
}

func (info *projectManifestInfo) enforceRunMode(mode string) error {
	if info == nil || len(info.file.RunModes) == 0 {
		return nil
	}
	if !info.file.AllowsRunMode(mode) {
		return fmt.Errorf("project manifest %s does not allow run mode %q (allowed: %s)", info.path, mode, info.file.RunModesText())
	}
	return nil
}

func (info *projectManifestInfo) enforceApprovals() error {
	if info == nil {
		return nil
	}
	if point, unsupported := info.file.UnsupportedApprovalPoint(); unsupported {
		return fmt.Errorf("project manifest %s declares approval point %q, but broker escalation approvals are not implemented; failing closed", info.path, point)
	}
	return nil
}

func (info *projectManifestInfo) manifest() *manifest.File {
	if info == nil {
		return nil
	}
	return info.file
}

func repoWorktreeRoot(repoPath string) string {
	out, err := exec.Command("git", "-C", repoPath, "rev-parse", "--show-toplevel").Output()
	root := strings.TrimSpace(string(out))
	if err != nil || root == "" {
		return repoPath
	}
	return root
}

func validateMaxRetries(value int) error {
	if value < 0 || value > 10 {
		return fmt.Errorf("--max-retries must be between 0 and 10")
	}
	return nil
}

func validateTokenBudgetRequest(warnAt int64, stopAt int64) error {
	if warnAt < 0 {
		return fmt.Errorf("--token-warn-at must be zero or greater")
	}
	if stopAt < 0 {
		return fmt.Errorf("--token-stop-at must be zero or greater")
	}
	if warnAt > 0 && stopAt > 0 && warnAt > stopAt {
		return fmt.Errorf("--token-warn-at must be less than or equal to --token-stop-at")
	}
	return nil
}

func agentCommandMatchesTool(commandName string, tool string) bool {
	return agentcaps.CommandMatchesTool(commandName, tool)
}

func resolveBrokerSocketPath(override string) (string, error) {
	if override != "" {
		return paths.ValidateSocketPath(override, "broker socket path")
	}
	socketPath, _, err := paths.BrokerClientSocket()
	return socketPath, err
}

func resolveCredentialHelper(override string) (string, error) {
	credentialHelper := override
	var err error
	if credentialHelper == "" {
		credentialHelper, err = resolveOptionalBinary("ai-agent-credential-helper")
		if err != nil || credentialHelper == "" {
			return "", fmt.Errorf("ai-agent-credential-helper not found next to ai-agent or in PATH; install it or use --credential-helper")
		}
	}
	if _, err := os.Stat(credentialHelper); err != nil {
		return "", fmt.Errorf("credential helper not found at %s: %w", credentialHelper, err)
	}
	return credentialHelper, nil
}

func resolveOptionalBinary(name string) (string, error) {
	return binresolve.ResolveOptional(name)
}

func resolveExecutableFromPath(name string, skipPath string) (string, error) {
	return binresolve.ResolveExecutableFromPath(name, skipPath)
}

func resolveRealGhPath(ghWrapper string) string {
	if p := os.Getenv(paths.EnvRealGh); binresolve.IsExecutableFile(p) {
		return p
	}
	p, _ := resolveExecutableFromPath("gh", ghWrapper)
	return p
}

func plannedResources(slug string, observability string, manifestResources []string) ([]plan.ProviderResource, []plan.ProviderResource, error) {
	github, err := capabilities.ResourceURI("github", "repo", slug)
	if err != nil {
		return nil, nil, err
	}
	resources := []plan.ProviderResource{plannedResource(github)}
	seen := map[string]struct{}{github.URI: {}}
	sinkSeen := map[string]struct{}{}
	var sinks []plan.ProviderResource
	var manifestSinks []plan.ProviderResource
	for _, raw := range manifestResources {
		resource, err := capabilities.ParseResourceURI(raw)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid project manifest resource %q: %w", raw, err)
		}
		if _, dup := seen[resource.URI]; dup {
			continue
		}
		planned := plannedResource(resource)
		resources = append(resources, planned)
		seen[resource.URI] = struct{}{}
		if sink, err := capabilities.ObservabilitySink(resource.URI); err == nil {
			manifestSinks = append(manifestSinks, plannedResource(sink))
		}
	}
	if observability != "" {
		sink, err := capabilities.ObservabilitySink(observability)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid observability resource %q", observability)
		}
		plannedSink := plannedResource(sink)
		if _, dup := seen[plannedSink.URI]; !dup {
			resources = append(resources, plannedSink)
			seen[plannedSink.URI] = struct{}{}
		}
		sinks = append(sinks, plannedSink)
		sinkSeen[plannedSink.URI] = struct{}{}
	}
	for _, sink := range manifestSinks {
		if _, dup := sinkSeen[sink.URI]; dup {
			continue
		}
		sinks = append(sinks, sink)
		sinkSeen[sink.URI] = struct{}{}
	}
	return resources, sinks, nil
}

func plannedBudgets(request RunRequest, manifestBudgets []manifest.ResourceBudget) ([]plan.Budget, error) {
	budgets := make([]plan.Budget, 0, len(manifestBudgets)+1)
	for _, budget := range manifestBudgets {
		budgets = append(budgets, plannedManifestBudget(budget))
	}
	if request.TokenWarnAt != 0 || request.TokenStopAt != 0 {
		for _, budget := range budgets {
			if budget.Name == plan.BudgetNameTokens {
				return nil, fmt.Errorf("project manifest resource budget %q collides with the CLI token budget name", budget.Name)
			}
		}
		budgets = append(budgets, plannedTokenBudget(plan.BudgetNameTokens, request.TokenWarnAt, request.TokenStopAt))
	}
	if len(budgets) == 0 {
		return nil, nil
	}
	if telemetry, ok := agentcaps.NativeTelemetryForCommand(request.AgentCommand); !ok || !telemetry.Supported {
		return nil, fmt.Errorf("token budgets require an agent command with native telemetry support")
	}
	return budgets, nil
}

func plannedManifestBudget(budget manifest.ResourceBudget) plan.Budget {
	stopPolicy := plan.BudgetStopPolicy(budget.StopPolicy)
	if stopPolicy == "" {
		stopPolicy = plan.BudgetStopPolicyWarnOnly
		if budget.StopAt > 0 {
			stopPolicy = plan.BudgetStopPolicyStopRun
		}
	}
	measurementSource := plan.BudgetMeasurementSource(budget.MeasurementSource)
	if measurementSource == "" {
		measurementSource = plan.BudgetMeasurementSourceNativeOTEL
	}
	return plan.Budget{
		Name:              budget.Name,
		Metric:            plan.BudgetMetric(budget.Metric),
		MeasurementSource: measurementSource,
		WarnAt:            budget.WarnAt,
		StopAt:            budget.StopAt,
		StopPolicy:        stopPolicy,
	}
}

func plannedTokenBudget(name string, warnAt int64, stopAt int64) plan.Budget {
	stopPolicy := plan.BudgetStopPolicyWarnOnly
	if stopAt > 0 {
		stopPolicy = plan.BudgetStopPolicyStopRun
	}
	return plan.Budget{
		Name:              name,
		Metric:            plan.BudgetMetricTokens,
		MeasurementSource: plan.BudgetMeasurementSourceNativeOTEL,
		WarnAt:            warnAt,
		StopAt:            stopAt,
		StopPolicy:        stopPolicy,
	}
}

func plannedResource(resource capabilities.Resource) plan.ProviderResource {
	return plan.ProviderResource{URI: resource.URI, Provider: resource.Provider, Kind: resource.Kind, Identifier: resource.Identifier}
}

func plannedInterceptionProfiles(session interception.Session) []plan.InterceptionProfile {
	registry := capabilities.InterceptionProfiles()
	result := make([]plan.InterceptionProfile, 0, len(registry))
	for _, profile := range registry {
		result = append(result, plan.InterceptionProfile{
			Provider:         profile.Provider,
			Commands:         append([]string(nil), profile.Commands...),
			ScrubEnv:         append([]string(nil), profile.ScrubEnv...),
			ScrubEnvPrefixes: append([]string(nil), profile.ScrubEnvPrefixes...),
			FailClosedEnv:    plannedEnvironmentVariables(profile.FailClosedEnv, session),
		})
	}
	return result
}

func plannedEnvironmentVariables(resolve func(interception.Session) []string, session interception.Session) []plan.EnvironmentVariable {
	if resolve == nil {
		return nil
	}
	env := resolve(session)
	result := make([]plan.EnvironmentVariable, 0, len(env))
	for _, entry := range env {
		name, value, ok := strings.Cut(entry, "=")
		if !ok || name == "" {
			continue
		}
		result = append(result, plan.EnvironmentVariable{Name: name, Value: value})
	}
	return result
}

func plannedCommandWrappers(ghWrapper string) []plan.CommandWrapper {
	if ghWrapper == "" {
		return nil
	}
	provider, ok := capabilities.ProviderForCommand("gh")
	if !ok {
		return nil
	}
	return []plan.CommandWrapper{{Provider: provider, Command: "gh", Path: ghWrapper}}
}

func projectedHomePaths() []plan.ProjectedPath {
	specs := agentcaps.ProjectedHomePaths()
	paths := make([]plan.ProjectedPath, 0, len(specs))
	for _, spec := range specs {
		paths = append(paths, plan.ProjectedPath{
			Name:    spec.Name,
			Kind:    projectedPathKind(spec.Kind),
			Exclude: append([]string(nil), spec.Exclude...),
		})
	}
	return paths
}

func projectedPathKind(kind agentcaps.ProjectedPathKind) plan.ProjectedPathKind {
	switch kind {
	case agentcaps.ProjectedPathDir:
		return plan.ProjectedPathDir
	case agentcaps.ProjectedPathFile:
		return plan.ProjectedPathFile
	default:
		return plan.ProjectedPathKind(kind)
	}
}

func homeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return home
}

func plannedQualityContracts(request RunRequest, repoRoot string, contracts []VerifyContract, contractsDir string) []plan.QualityContract {
	if request.VerifyCommand != "" {
		return []plan.QualityContract{plannedQualityContract(VerifyContract{Name: "verify-cmd", Command: request.VerifyCommand, FailurePolicy: plan.QualityFailurePolicyRetryAgent}, repoRoot)}
	}
	result := make([]plan.QualityContract, 0, len(contracts))
	for _, contract := range contracts {
		result = append(result, plannedQualityContract(contract, contractsDir))
	}
	return result
}

func plannedQualityContract(contract VerifyContract, workDir string) plan.QualityContract {
	return plan.QualityContract{
		Name:            contract.Name,
		Command:         contract.Command,
		WorkDir:         workDir,
		FailurePolicy:   contract.FailurePolicy,
		TailLines:       60,
		EvidenceDir:     filepath.Join(paths.ConfigDir(), "evidence"),
		EvidenceMaxRuns: 20,
	}
}

func qualityFailurePolicy(retry string) plan.QualityFailurePolicy {
	if retry == manifest.RetryNever {
		return plan.QualityFailurePolicyFailRun
	}
	return plan.QualityFailurePolicyRetryAgent
}
