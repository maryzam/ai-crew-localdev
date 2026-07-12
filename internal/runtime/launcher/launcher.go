package launcher

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/broker/api"
	"github.com/maryzam/ai-crew-localdev/internal/broker/client"
	"github.com/maryzam/ai-crew-localdev/internal/control/plan"
	"github.com/maryzam/ai-crew-localdev/internal/platform/paths"
	"github.com/maryzam/ai-crew-localdev/internal/platform/telemetry"
	"github.com/maryzam/ai-crew-localdev/internal/quality"
	"github.com/maryzam/ai-crew-localdev/internal/runtime/homestate"
)

var execCommand = exec.Command

const childBindFD = 3

type AgentExitError struct {
	err  error
	code int
}

func (e *AgentExitError) Error() string {
	return fmt.Sprintf("agent exited with error: %v", e.err)
}

func (e *AgentExitError) Unwrap() error {
	return e.err
}

func (e *AgentExitError) ExitCode() int {
	return e.code
}

type brokerClient interface {
	CreateSession(api.CreateSessionRequest) (*api.CreateSessionResponse, error)
	PublishTelemetry(api.PublishTelemetryRequest) (*api.PublishTelemetryResponse, error)
	RevokeSession(api.RevokeSessionRequest) error
}

var newBrokerClient = func(socketPath string) brokerClient {
	return &client.Client{SocketPath: socketPath}
}

type Options struct {
	AIAgentVersion string
}

type executionPlan struct {
	RunID                 string
	AgentName             string
	AgentType             string
	ConfiguredModel       string
	AgentCommandName      string
	TaskRef               string
	RepoSlug              string
	RepoPath              string
	SocketPath            string
	RealGhPath            string
	AgentCommand          []string
	AIAgentVersion        string
	Resources             []string
	ObservabilityResource string
	InterceptionProfiles  []plan.InterceptionProfile
	CommandWrappers       []plan.CommandWrapper
	SourceHome            string
	ProjectedHomePaths    []homestate.ProjectedPath
	CleanupHome           bool
	QualityContracts      []plan.QualityContract
	Retry                 plan.Retry
	Model                 plan.ModelAttribution
}

func Launch(runPlan plan.RunPlan, opts Options) (returnErr error) {
	if os.Getenv(paths.EnvContainer) != "1" {
		return fmt.Errorf("managed runs are devcontainer-only; start the devcontainer with ai-agent up and run ai-agent run inside it")
	}
	snapshot := runPlan.Snapshot()
	if errs := plan.Validate(snapshot); errs.HasErrors() {
		return fmt.Errorf("invalid run plan: %w", errs)
	}
	execPlan := executionPlanFromDraft(snapshot, opts)
	rec, err := telemetry.StartRun(telemetry.RunContext{
		RunID:           execPlan.RunID,
		TaskRef:         execPlan.TaskRef,
		AgentName:       execPlan.AgentName,
		Agent:           telemetryAgent(execPlan),
		ConfiguredModel: execPlan.ConfiguredModel,
		Model:           telemetryModel(execPlan.Model),
		Repo:            execPlan.RepoSlug,
		HostRepoPath:    execPlan.RepoPath,
		AgentCommand:    execPlan.AgentCommand,
		VerifyEnabled:   len(execPlan.QualityContracts) > 0,
		MaxRetries:      execPlan.Retry.MaxAgentRetries,
		AIAgentVersion:  execPlan.AIAgentVersion,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: managed-run telemetry disabled: %v\n", err)
	}
	terminalPhase := telemetry.PhaseSessionCreate
	defer func() {
		if !rec.Finished() {
			outcome := telemetry.OutcomeLaunchFailed
			if terminalPhase == telemetry.PhaseSessionCreate {
				outcome = telemetry.OutcomeSessionCreateFailed
			}
			if returnErr != nil {
				rec.SetDiagnostic(outcome, returnErr.Error())
			}
			rec.Finish(outcome, terminalPhase, exitCodePointer(returnErr), 0)
		}
		_ = rec.Close()
		printRunSummary(rec.Summary())
	}()
	client := newBrokerClient(execPlan.SocketPath)
	resp, err := client.CreateSession(api.CreateSessionRequest{
		AgentName:    execPlan.AgentName,
		HostRepoPath: execPlan.RepoPath,
		Resources:    execPlan.Resources,
		RunID:        execPlan.RunID,
		TaskRef:      execPlan.TaskRef,
	})
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	rec.SetSessionID(resp.SessionID)

	closeRelay := func() {}
	revoke := func() {
		closeRelay()
		rec.CloseOTLP()
		if err := client.RevokeSession(api.RevokeSessionRequest{
			SessionID:  resp.SessionID,
			BindSecret: resp.BindSecret,
		}); err != nil {
			fmt.Fprintf(os.Stderr, "warning: revoke broker session %s: %v\n", resp.SessionID, err)
			rec.SetDiagnostic("session_revoke_failed", err.Error())
			return
		}
		rec.SessionRevoked()
	}

	if err := SaveSessionInfo(SessionInfo{
		SessionID:  resp.SessionID,
		AgentName:  execPlan.AgentName,
		Repo:       execPlan.RepoSlug,
		SocketPath: execPlan.SocketPath,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not save session info: %v\n", err)
	}

	terminalPhase = telemetry.PhaseBindSetup
	bindFD, err := CreateBindFD(resp.BindSecret)
	if err != nil {
		revoke()
		return fmt.Errorf("create bind FD: %w", err)
	}
	bindFile := os.NewFile(uintptr(bindFD), "ai-agent-session-bind")
	if bindFile == nil {
		_ = syscall.Close(bindFD)
		revoke()
		return fmt.Errorf("create bind file: invalid fd %d", bindFD)
	}
	defer func() { _ = bindFile.Close() }()

	terminalPhase = telemetry.PhaseWrapperSetup
	ghWrapperDir, skippedProviders, cleanupGh, err := prepareCommandWrappers(
		execPlan.CommandWrappers,
		execPlan.InterceptionProfiles,
	)
	if err != nil {
		revoke()
		return fmt.Errorf("prepare command wrappers: %w", err)
	}
	defer cleanupGh()
	for _, provider := range skippedProviders {
		fmt.Fprintf(os.Stderr, "warning: no command wrapper configured for provider %s; its declared commands are not interposed on PATH\n", provider)
	}

	baseEnv := withEnvValue(os.Environ(), "HOME", execPlan.SourceHome)
	env := ScrubEnv(
		baseEnv,
		execPlan.InterceptionProfiles,
		execPlan.SocketPath,
		resp.SessionID,
		childBindFD,
		execPlan.RepoSlug,
		ghWrapperDir,
		execPlan.RealGhPath,
	)
	env = append(env, paths.EnvRunID+"="+execPlan.RunID)
	if execPlan.TaskRef != "" {
		env = append(env, paths.EnvTaskRef+"="+execPlan.TaskRef)
	}
	finalizeHome := noopHomeFinalizer
	if execPlan.CleanupHome {
		projection, homeErr := homestate.Prepare(homestate.EnvValue(env, "HOME"), execPlan.ProjectedHomePaths)
		if homeErr != nil {
			revoke()
			return fmt.Errorf("prepare isolated run home: %w", homeErr)
		}
		homeFinalized := false
		finalizeHome = func(rec *telemetry.Recorder) error {
			if homeFinalized {
				return nil
			}
			homeFinalized = true
			commitErr := projection.Commit()
			for _, warning := range projection.Warnings() {
				fmt.Fprintf(os.Stderr, "warning: isolated home state: %s\n", warning)
			}
			if len(projection.Warnings()) > 0 && commitErr == nil {
				rec.SetDiagnostic("home_state_drift", strings.Join(projection.Warnings(), "; "))
			}
			cleanupErr := projection.Cleanup()
			if commitErr != nil {
				fmt.Fprintf(os.Stderr, "warning: persist isolated home state: %v\n", commitErr)
				rec.SetDiagnostic("home_state_commit_failed", commitErr.Error())
				if cleanupErr != nil {
					return fmt.Errorf("persist isolated home state: %w; cleanup isolated home: %v", commitErr, cleanupErr)
				}
				return fmt.Errorf("persist isolated home state: %w", commitErr)
			}
			if cleanupErr != nil {
				fmt.Fprintf(os.Stderr, "warning: cleanup isolated home: %v\n", cleanupErr)
				rec.SetDiagnostic("home_state_cleanup_failed", cleanupErr.Error())
				return fmt.Errorf("cleanup isolated home: %w", cleanupErr)
			}
			return nil
		}
		defer func() {
			if !homeFinalized {
				if err := projection.Cleanup(); err != nil {
					fmt.Fprintf(os.Stderr, "warning: cleanup isolated home: %v\n", err)
				}
			}
		}()
		env = homestate.ApplyEnv(env, projection.RunHome())
	}
	agentBin, err := exec.LookPath(execPlan.AgentCommand[0])
	if err != nil {
		revoke()
		terminalPhase = telemetry.PhaseAgentStart
		return fmt.Errorf("agent binary not found: %w", err)
	}
	var exporter telemetry.OTLPExporter
	if execPlan.ObservabilityResource != "" {
		exporter = &brokerTelemetryExporter{
			client:     client,
			sessionID:  resp.SessionID,
			bindSecret: resp.BindSecret,
			resource:   execPlan.ObservabilityResource,
		}
	}
	if nativeTelemetrySupported(execPlan.AgentCommand) || exporter != nil {
		relay, relayErr := telemetry.StartNativeRelay(rec, exporter)
		if relayErr != nil {
			fmt.Fprintf(os.Stderr, "warning: native telemetry disabled: %v\n", relayErr)
		} else {
			closeRelay = relay.Close
			env = nativeTelemetryEnv(env, execPlan.AgentCommand, relay, execPlan.RunID)
			execPlan.AgentCommand = nativeTelemetryCommand(execPlan.AgentCommand, relay)
		}
	}
	fmt.Fprintf(os.Stderr, "run %s session %s created for %s on %s (expires %s)\n",
		execPlan.RunID, resp.SessionID, execPlan.AgentName, execPlan.RepoSlug, resp.ExpiresAt.Format("15:04:05"))

	if len(execPlan.QualityContracts) > 0 {
		terminalPhase = telemetry.PhaseVerify
		return launchWithVerify(agentBin, execPlan, env, bindFile, resp.SessionID, revoke, rec, finalizeHome)
	}
	terminalPhase = telemetry.PhaseAgent
	return superviseAgent(agentBin, execPlan, env, bindFile, resp.SessionID, revoke, rec, finalizeHome)
}

func noopHomeFinalizer(*telemetry.Recorder) error {
	return nil
}

func superviseAgent(agentBin string, execPlan executionPlan, env []string, bindFile *os.File, sessionID string, revoke func(), rec *telemetry.Recorder, finalizeHome func(*telemetry.Recorder) error) error {
	agentCmd := newAgentCommand(agentBin, execPlan.AgentCommand, env, bindFile)
	rec.AgentStarted(1)
	start := time.Now()
	if err := agentCmd.Start(); err != nil {
		rec.AgentFinished(1, "start_failed", nil, time.Since(start))
		homeErr := finalizeHome(rec)
		rec.Finish(telemetry.OutcomeAgentFailed, telemetry.PhaseAgentStart, nil, 0)
		cleanup(sessionID, revoke)
		return errors.Join(fmt.Errorf("start agent: %w", err), homeErr)
	}

	stopForwarding := forwardSignals(agentCmd)
	defer stopForwarding()

	err := agentCmd.Wait()
	exit := exitCodePointer(err)
	if err != nil {
		rec.AgentFinished(1, "failed", exit, time.Since(start))
	} else {
		rec.AgentFinished(1, "passed", exit, time.Since(start))
	}
	homeErr := finalizeHome(rec)
	if err != nil {
		rec.Finish(recordAgentFailure(rec, err), telemetry.PhaseAgent, exit, 0)
	} else {
		if homeErr != nil {
			rec.Finish(telemetry.OutcomeLaunchFailed, telemetry.PhaseCleanup, nil, 0)
			cleanup(sessionID, revoke)
			return homeErr
		}
		rec.Finish(telemetry.OutcomePassed, telemetry.PhaseAgent, exit, 0)
	}
	cleanup(sessionID, revoke)
	if err != nil {
		return errors.Join(agentExitError(err), homeErr)
	}
	return nil
}

func newAgentCommand(agentBin string, agentCommand []string, env []string, bindFile *os.File) *exec.Cmd {
	agentCmd := execCommand(agentBin, agentCommand[1:]...)
	agentCmd.Env = env
	agentCmd.Stdin = os.Stdin
	agentCmd.Stdout = os.Stdout
	agentCmd.Stderr = os.Stderr
	attachBindFile(agentCmd, bindFile)
	return agentCmd
}

func attachBindFile(cmd *exec.Cmd, bindFile *os.File) {
	if bindFile != nil {
		cmd.ExtraFiles = []*os.File{bindFile}
	}
}

func agentExitError(err error) error {
	code, ok := exitCode(err)
	if !ok {
		return fmt.Errorf("agent exited with error: %w", err)
	}
	return &AgentExitError{err: err, code: code}
}

func exitCode(err error) (int, bool) {
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		return 0, false
	}
	if status, ok := exitErr.Sys().(syscall.WaitStatus); ok {
		if status.Exited() {
			return status.ExitStatus(), true
		}
		if status.Signaled() {
			return 128 + int(status.Signal()), true
		}
	}
	if code := exitErr.ExitCode(); code >= 0 {
		return code, true
	}
	return 1, true
}

func forwardSignals(agentCmd *exec.Cmd) (stop func()) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP, syscall.SIGQUIT)
	go func() {
		for sig := range sigCh {
			if p := agentCmd.Process; p != nil {
				_ = p.Signal(sig)
			}
		}
	}()
	return func() { signal.Stop(sigCh); close(sigCh) }
}

func cleanup(sessionID string, revoke func()) {
	revoke()
	_ = RemoveSessionInfo(sessionID)
}

func launchWithVerify(agentBin string, execPlan executionPlan, env []string, bindFile *os.File, sessionID string, revoke func(), rec *telemetry.Recorder, finalizeHome func(*telemetry.Recorder) error) error {
	maxAttempts := execPlan.Retry.Attempts()

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		agentCmd := newAgentCommand(agentBin, execPlan.AgentCommand, env, bindFile)
		rec.AgentStarted(attempt)
		agentStart := time.Now()
		if err := runCommandWithSignals(agentCmd); err != nil {
			exit := exitCodePointer(err)
			rec.AgentFinished(attempt, "failed", exit, time.Since(agentStart))
			homeErr := finalizeHome(rec)
			rec.Finish(recordAgentFailure(rec, err), telemetry.PhaseAgent, exit, 0)
			cleanup(sessionID, revoke)
			return errors.Join(agentExitError(err), homeErr)
		}
		rec.AgentFinished(attempt, "passed", intPtr(0), time.Since(agentStart))

		failed, result, err := runContracts(execPlan.QualityContracts, attempt, maxAttempts, env, bindFile, rec)
		if err != nil {
			homeErr := finalizeHome(rec)
			rec.Finish(telemetry.OutcomeVerifyFailed, telemetry.PhaseVerify, nil, 0)
			cleanup(sessionID, revoke)
			return errors.Join(err, homeErr)
		}
		if failed == nil {
			homeErr := finalizeHome(rec)
			if homeErr != nil {
				rec.Finish(telemetry.OutcomeLaunchFailed, telemetry.PhaseCleanup, nil, 0)
				cleanup(sessionID, revoke)
				return homeErr
			}
			fmt.Fprintln(os.Stderr, "verify: passed")
			rec.Finish(telemetry.OutcomePassed, telemetry.PhaseVerify, intPtr(0), 0)
			cleanup(sessionID, revoke)
			return nil
		}

		exit := intPtr(result.ExitCode)
		if result.FailureClass == quality.FailureClassSignal {
			homeErr := finalizeHome(rec)
			rec.SetSignal(result.Signal)
			rec.Finish(telemetry.OutcomeInterrupted, telemetry.PhaseVerify, exit, 0)
			cleanup(sessionID, revoke)
			return errors.Join(&AgentExitError{
				err:  fmt.Errorf("contract %q interrupted by signal %s", failed.Name, result.Signal),
				code: result.ExitCode,
			}, homeErr)
		}
		switch failed.FailurePolicy {
		case plan.QualityFailurePolicyRetryAgent:
			if attempt < maxAttempts {
				fmt.Fprintf(os.Stderr, "verify: contract %q failed, re-launching agent (retry %d/%d)\n", failed.Name, attempt, execPlan.Retry.MaxAgentRetries)
			}
		case plan.QualityFailurePolicyFailRun:
			homeErr := finalizeHome(rec)
			rec.Finish(telemetry.OutcomeVerifyFailed, telemetry.PhaseVerify, exit, 0)
			cleanup(sessionID, revoke)
			return errors.Join(fmt.Errorf("contract %q failed with planned failure policy %q", failed.Name, failed.FailurePolicy), homeErr)
		default:
			homeErr := finalizeHome(rec)
			rec.Finish(telemetry.OutcomeVerifyFailed, telemetry.PhaseVerify, exit, 0)
			cleanup(sessionID, revoke)
			return errors.Join(fmt.Errorf("contract %q failed with planned failure policy %q", failed.Name, failed.FailurePolicy), homeErr)
		}
	}

	homeErr := finalizeHome(rec)
	rec.Finish(telemetry.OutcomeVerifyFailed, telemetry.PhaseVerify, nil, 0)
	cleanup(sessionID, revoke)
	return errors.Join(fmt.Errorf("verification failed after %d attempt(s)", maxAttempts), homeErr)
}

func runContracts(contracts []plan.QualityContract, attempt int, maxAttempts int, env []string, bindFile *os.File, rec *telemetry.Recorder) (*plan.QualityContract, quality.CheckResult, error) {
	var extraFiles []*os.File
	if bindFile != nil {
		extraFiles = []*os.File{bindFile}
	}
	for i := range contracts {
		contract := &contracts[i]
		fmt.Fprintf(os.Stderr, "verify: contract %q running %q (attempt %d/%d)\n", contract.Name, contract.Command, attempt, maxAttempts)
		rec.VerifyStarted(attempt, contract.Name, contract.Command)
		result, checkErr := quality.RunCheck(quality.CheckOptions{
			Command:          []string{"sh", "-c", contract.Command},
			Dir:              contract.WorkDir,
			Env:              env,
			Stdin:            os.Stdin,
			ExtraFiles:       extraFiles,
			EvidenceDir:      contract.EvidenceDir,
			EvidenceMaxFiles: contract.EvidenceMaxRuns,
			TailLines:        contract.TailLines,
			Run:              runCommandWithSignals,
		})
		if checkErr != nil {
			rec.VerifyFinished(attempt, contract.Name, "failed", quality.FailureClassStartFailed, nil, 0)
			return nil, quality.CheckResult{}, fmt.Errorf("run contract %q: %w", contract.Name, checkErr)
		}
		if result.EvidenceCleanupErr != nil {
			fmt.Fprintf(os.Stderr, "warning: verify evidence retention: %v\n", result.EvidenceCleanupErr)
			rec.SetDiagnostic("verify_evidence_retention_failed", result.EvidenceCleanupErr.Error())
		}
		if result.Passed {
			rec.VerifyFinished(attempt, contract.Name, "passed", "", intPtr(0), result.Duration)
			continue
		}
		printVerifyTail(result.FailureTail)
		if result.LogPath != "" {
			fmt.Fprintf(os.Stderr, "verify: full output saved to %s\n", result.LogPath)
		}
		rec.VerifyFinished(attempt, contract.Name, "failed", result.FailureClass, intPtr(result.ExitCode), result.Duration)
		return contract, result, nil
	}
	return nil, quality.CheckResult{}, nil
}

func printVerifyTail(lines []string) {
	if len(lines) == 0 {
		return
	}
	fmt.Fprintln(os.Stderr, "verify: failure tail")
	for _, line := range lines {
		fmt.Fprintln(os.Stderr, line)
	}
}

func runCommandWithSignals(command *exec.Cmd) error {
	if err := command.Start(); err != nil {
		return err
	}
	stopForwarding := forwardSignals(command)
	err := command.Wait()
	stopForwarding()
	return err
}

func exitCodePointer(err error) *int {
	if err == nil {
		return intPtr(0)
	}
	if code, ok := exitCode(err); ok {
		return intPtr(code)
	}
	return nil
}

func recordAgentFailure(rec *telemetry.Recorder, err error) string {
	if signalName, interrupted := interruptedSignal(err); interrupted {
		rec.SetSignal(signalName)
		return telemetry.OutcomeInterrupted
	}
	return telemetry.OutcomeAgentFailed
}

func interruptedSignal(err error) (string, bool) {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			return status.Signal().String(), true
		}
	}
	return "", false
}

func intPtr(v int) *int {
	return &v
}

func executionPlanFromDraft(snapshot plan.Draft, opts Options) executionPlan {
	resources := make([]string, 0, len(snapshot.Broker.Resources))
	for _, resource := range snapshot.Broker.Resources {
		resources = append(resources, resource.URI)
	}
	observabilityResource := ""
	if len(snapshot.Telemetry.ObservabilitySinks) > 0 {
		observabilityResource = snapshot.Telemetry.ObservabilitySinks[0].URI
	}
	return executionPlan{
		RunID:                 snapshot.RunID,
		AgentName:             snapshot.Agent.Name,
		AgentType:             snapshot.Agent.Type,
		ConfiguredModel:       snapshot.Agent.ConfiguredModel,
		AgentCommandName:      snapshot.Agent.CommandName,
		TaskRef:               snapshot.TaskRef,
		RepoSlug:              snapshot.Repository.Slug,
		RepoPath:              snapshot.Repository.RootPath,
		SocketPath:            snapshot.Broker.SocketPath,
		RealGhPath:            snapshot.Env.RealGhPath,
		AgentCommand:          append([]string(nil), snapshot.Agent.Command...),
		AIAgentVersion:        opts.AIAgentVersion,
		Resources:             resources,
		ObservabilityResource: observabilityResource,
		InterceptionProfiles:  append([]plan.InterceptionProfile(nil), snapshot.Intercept.Profiles...),
		CommandWrappers:       append([]plan.CommandWrapper(nil), snapshot.Intercept.Wrappers...),
		SourceHome:            snapshot.Home.SourceHome,
		ProjectedHomePaths:    plannedHomePaths(snapshot.Home.ProjectedPaths),
		CleanupHome:           snapshot.Cleanup.CleanupHome,
		QualityContracts:      append([]plan.QualityContract(nil), snapshot.Quality.Contracts...),
		Retry:                 snapshot.Retry,
		Model:                 snapshot.Agent.Model,
	}
}

func telemetryAgent(execPlan executionPlan) telemetry.AgentMetadata {
	return telemetry.AgentMetadata{Type: execPlan.AgentType, Identity: execPlan.AgentName, Command: execPlan.AgentCommandName}
}

func telemetryModel(model plan.ModelAttribution) telemetry.ModelAttribution {
	return telemetry.ModelAttribution{
		Provider:  model.Provider,
		Family:    model.Family,
		Requested: model.Requested,
		Resolution: telemetry.ModelResolution{
			Status:        model.Resolution.Status,
			Confidence:    model.Resolution.Confidence,
			PrimarySource: model.Resolution.PrimarySource,
			Sources:       append([]string(nil), model.Resolution.Sources...),
			Conflict:      model.Resolution.Conflict,
		},
	}
}

func plannedHomePaths(paths []plan.ProjectedPath) []homestate.ProjectedPath {
	result := make([]homestate.ProjectedPath, 0, len(paths))
	for _, path := range paths {
		result = append(result, homestate.ProjectedPath{
			Name:    path.Name,
			Kind:    homestate.ProjectedPathKind(path.Kind),
			Exclude: append([]string(nil), path.Exclude...),
		})
	}
	return result
}

func withEnvValue(env []string, name string, value string) []string {
	prefix := name + "="
	result := make([]string, 0, len(env)+1)
	replaced := false
	for _, item := range env {
		if strings.HasPrefix(item, prefix) {
			if !replaced {
				result = append(result, prefix+value)
				replaced = true
			}
			continue
		}
		result = append(result, item)
	}
	if !replaced {
		result = append(result, prefix+value)
	}
	return result
}

func printRunSummary(summary telemetry.RunSummary) {
	if summary.RunID == "" || summary.Outcome == "" {
		return
	}
	fmt.Fprintf(os.Stderr, "run %s: %s during %s (%s)\n",
		telemetry.ShortRunID(summary.RunID), summary.Outcome, summary.TerminalPhase,
		time.Duration(summary.DurationMS)*time.Millisecond)
	fmt.Fprintf(os.Stderr, "inspect: ai-agent runs show %s\n", summary.RunID)
}

func prepareCommandWrappers(wrappers []plan.CommandWrapper, profs []plan.InterceptionProfile) (dir string, skipped []string, cleanup func(), err error) {
	noop := func() {}
	wrapperByProvider := map[string]string{}
	for _, wrapper := range wrappers {
		wrapperByProvider[wrapper.Provider] = wrapper.Path
	}
	links := map[string]string{}
	for _, profile := range profs {
		if len(profile.Commands) == 0 {
			continue
		}
		wrapperPath := wrapperByProvider[profile.Provider]
		if wrapperPath == "" {
			skipped = append(skipped, profile.Provider)
			continue
		}

		absWrapper, err := filepath.Abs(wrapperPath)
		if err != nil {
			return "", nil, noop, fmt.Errorf("resolve %s command wrapper path: %w", profile.Provider, err)
		}
		if _, err := os.Stat(absWrapper); err != nil {
			return "", nil, noop, fmt.Errorf("%s command wrapper not found at %s: %w", profile.Provider, absWrapper, err)
		}

		for _, command := range profile.Commands {
			if existing, dup := links[command]; dup && existing != absWrapper {
				return "", nil, noop, fmt.Errorf("command %q is interposed by multiple provider wrappers", command)
			}
			links[command] = absWrapper
		}
	}

	if len(links) == 0 {
		return "", skipped, noop, nil
	}

	dir, err = os.MkdirTemp("", "ai-agent-shim-*")
	if err != nil {
		return "", nil, noop, fmt.Errorf("create command wrapper dir: %w", err)
	}

	for command, target := range links {
		link := filepath.Join(dir, command)
		if err := os.Symlink(target, link); err != nil {
			_ = os.RemoveAll(dir)
			return "", nil, noop, fmt.Errorf("create %s symlink: %w", command, err)
		}
	}

	return dir, skipped, func() { _ = os.RemoveAll(dir) }, nil
}
