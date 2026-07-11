package control

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/maryzam/ai-crew-localdev/internal/agentstate"
	"github.com/maryzam/ai-crew-localdev/internal/broker/api"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/identity"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/manifest"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/store"
	"github.com/maryzam/ai-crew-localdev/internal/control/plan"
	"github.com/maryzam/ai-crew-localdev/internal/platform/binresolve"
	"github.com/maryzam/ai-crew-localdev/internal/platform/correlation"
	"github.com/maryzam/ai-crew-localdev/internal/platform/paths"
	"github.com/maryzam/ai-crew-localdev/internal/platform/telemetry"
	"github.com/maryzam/ai-crew-localdev/internal/providers/profiles"
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
	IsolateHome              bool
	AgentCommand             []string
	AIAgentVersion           string
	ObservabilityResource    string
}

type PlannedRun struct {
	Plan     plan.RunPlan
	Launcher LauncherOptions
}

type LauncherOptions struct {
	RunID                 string
	AgentName             string
	ConfiguredModel       string
	TaskRef               string
	RepoPath              string
	SocketPath            string
	CredHelper            string
	GhWrapper             string
	RealGhPath            string
	AgentCommand          []string
	AIAgentVersion        string
	ObservabilityResource string
	VerifyCmd             string
	Contracts             []VerifyContract
	ContractsDir          string
	MaxRetries            int
	DisableHomeIsolation  bool
}

type VerifyContract struct {
	Name       string
	Command    string
	RetryAgent bool
}

type Planner struct {
	errOut io.Writer
}

type projectManifestInfo struct {
	file *manifest.File
	path string
	root string
}

type hostAgentIdentity struct {
	value identity.AgentIdentity
	found bool
	err   error
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
	info, err := loadProjectManifest(planner.errOut, request.RepoPath)
	if err != nil {
		return PlannedRun{}, err
	}
	hostIdentity := configuredIdentity(request.AgentName)
	if err := info.enforceAgent(request.AgentName, request.AgentCommand, hostIdentity); err != nil {
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
	configuredModel := hostIdentity.model()
	if manifestModel := info.modelDefault(request.AgentName); manifestModel != "" {
		configuredModel = manifestModel
		_, _ = fmt.Fprintf(planner.errOut, "model: run attribution uses project manifest default %q for agent %s\n", manifestModel, request.AgentName)
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
	resources, observabilitySinks, err := plannedResources(repo.Slug, request.ObservabilityResource)
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
			Tool:            hostIdentity.tool(),
			ConfiguredModel: configuredModel,
			Command:         request.AgentCommand,
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
			Profiles: plannedInterceptionProfiles(),
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
	launcher := launcherOptionsFromPlan(runPlan, request.AIAgentVersion, request.VerifyCommand, contracts, contractsDir, ghWrapper)
	return PlannedRun{Plan: runPlan, Launcher: launcher}, nil
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

func (info *projectManifestInfo) enforceAgent(agentName string, command []string, hostIdentity hostAgentIdentity) error {
	if info == nil || info.file.Agents == nil || len(info.file.Agents.Allowed) == 0 {
		return nil
	}
	if !slices.Contains(info.file.Agents.Allowed, agentName) {
		return fmt.Errorf("agent %q is not allowed by the project manifest %s (allowed: %s)", agentName, info.path, strings.Join(info.file.Agents.Allowed, ", "))
	}
	if hostIdentity.err != nil {
		return fmt.Errorf("agent %q is allowed by the project manifest %s but host identity could not be loaded: %w", agentName, info.path, hostIdentity.err)
	}
	if !hostIdentity.found {
		return fmt.Errorf("agent %q is allowed by the project manifest %s but no host identity is configured", agentName, info.path)
	}
	tool := strings.TrimSpace(hostIdentity.value.Tool)
	if tool == "" {
		return fmt.Errorf("agent %q is allowed by the project manifest %s but host identity has no configured tool", agentName, info.path)
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
			Name:       contract.Name,
			Command:    contract.Command,
			RetryAgent: contract.Retry != manifest.RetryNever,
		})
	}
	_, _ = fmt.Fprintf(errOut, "verify: %d project contract(s) declared in %s\n", len(contracts), info.path)
	return contracts, info.root
}

func configuredIdentity(agentName string) hostAgentIdentity {
	snapshot, err := store.Load(paths.DefaultIdentitiesPath(), paths.DefaultPolicyPath())
	if err != nil || snapshot.IdentitiesError != nil {
		if err == nil {
			err = snapshot.IdentitiesError
		}
		return hostAgentIdentity{err: err}
	}
	agent, ok := snapshot.Identities.Agents[agentName]
	if !ok {
		return hostAgentIdentity{}
	}
	return hostAgentIdentity{value: agent, found: true}
}

func (host hostAgentIdentity) model() string {
	if !host.found || host.err != nil {
		return ""
	}
	return strings.TrimSpace(host.value.Model)
}

func (host hostAgentIdentity) tool() string {
	if !host.found || host.err != nil {
		return ""
	}
	return strings.TrimSpace(host.value.Tool)
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

func agentCommandMatchesTool(commandName string, tool string) bool {
	commandName = filepath.Base(strings.TrimSpace(commandName))
	tool = filepath.Base(strings.TrimSpace(tool))
	switch tool {
	case "claude-code":
		return commandName == "claude" || commandName == "claude-code"
	default:
		return commandName == tool
	}
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

func plannedResources(slug string, observability string) ([]plan.ProviderResource, []plan.ProviderResource, error) {
	github := resourceForURI("github:repo:" + slug)
	resources := []plan.ProviderResource{github}
	var sinks []plan.ProviderResource
	if observability == "" {
		return resources, nil, nil
	}
	resource, err := api.ParseResourceURI(observability)
	if err != nil || resource.Provider != "langfuse" || resource.Kind != "project" {
		return nil, nil, fmt.Errorf("invalid observability resource %q", observability)
	}
	sink := plan.ProviderResource{URI: observability, Provider: resource.Provider, Kind: resource.Kind, Identifier: resource.Identifier}
	resources = append(resources, sink)
	sinks = append(sinks, sink)
	return resources, sinks, nil
}

func resourceForURI(uri string) plan.ProviderResource {
	resource, err := api.ParseResourceURI(uri)
	if err != nil {
		return plan.ProviderResource{URI: uri}
	}
	return plan.ProviderResource{URI: uri, Provider: resource.Provider, Kind: resource.Kind, Identifier: resource.Identifier}
}

func plannedInterceptionProfiles() []plan.InterceptionProfile {
	registry := profiles.All()
	result := make([]plan.InterceptionProfile, 0, len(registry))
	for _, profile := range registry {
		result = append(result, plan.InterceptionProfile{Provider: profile.Provider, Commands: profile.Commands})
	}
	return result
}

func plannedCommandWrappers(ghWrapper string) []plan.CommandWrapper {
	if ghWrapper == "" {
		return nil
	}
	return []plan.CommandWrapper{{Provider: "github", Command: "gh", Path: ghWrapper}}
}

func projectedHomePaths() []string {
	specs := agentstate.Specs()
	names := make([]string, 0, len(specs))
	for _, spec := range specs {
		names = append(names, spec.Name)
	}
	return names
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
		return []plan.QualityContract{plannedQualityContract(VerifyContract{Name: "verify-cmd", Command: request.VerifyCommand, RetryAgent: true}, repoRoot)}
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
		RetryAgent:      contract.RetryAgent,
		TailLines:       60,
		EvidenceDir:     filepath.Join(paths.ConfigDir(), "evidence"),
		EvidenceMaxRuns: 20,
	}
}

func launcherOptionsFromPlan(runPlan plan.RunPlan, version string, verifyCommand string, contracts []VerifyContract, contractsDir string, ghWrapper string) LauncherOptions {
	snapshot := runPlan.Snapshot()
	observabilityResource := ""
	if len(snapshot.Telemetry.ObservabilitySinks) > 0 {
		observabilityResource = snapshot.Telemetry.ObservabilitySinks[0].URI
	}
	return LauncherOptions{
		RunID:                 snapshot.RunID,
		AgentName:             snapshot.Agent.Name,
		ConfiguredModel:       snapshot.Agent.ConfiguredModel,
		TaskRef:               snapshot.TaskRef,
		RepoPath:              snapshot.Repository.RootPath,
		SocketPath:            snapshot.Broker.SocketPath,
		CredHelper:            snapshot.Env.CredentialHelperPath,
		GhWrapper:             ghWrapper,
		RealGhPath:            snapshot.Env.RealGhPath,
		AgentCommand:          append([]string(nil), snapshot.Agent.Command...),
		AIAgentVersion:        version,
		ObservabilityResource: observabilityResource,
		VerifyCmd:             verifyCommand,
		Contracts:             append([]VerifyContract(nil), contracts...),
		ContractsDir:          contractsDir,
		MaxRetries:            snapshot.Retry.MaxAgentRetries,
		DisableHomeIsolation:  !snapshot.Cleanup.CleanupHome,
	}
}
