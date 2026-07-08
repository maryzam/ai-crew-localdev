package launcher

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/broker/api"
	"github.com/maryzam/ai-crew-localdev/internal/broker/client"
	"github.com/maryzam/ai-crew-localdev/internal/interception"
	"github.com/maryzam/ai-crew-localdev/internal/platform/correlation"
	"github.com/maryzam/ai-crew-localdev/internal/platform/paths"
	"github.com/maryzam/ai-crew-localdev/internal/platform/telemetry"
	"github.com/maryzam/ai-crew-localdev/internal/providers/profiles"
	"github.com/maryzam/ai-crew-localdev/internal/quality"
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

	VerifyCmd            string
	Contracts            []VerifyContract
	ContractsDir         string
	MaxRetries           int
	DisableHomeIsolation bool
}

type VerifyContract struct {
	Name       string
	Command    string
	RetryAgent bool
}

func (o Options) verifyContracts() []VerifyContract {
	if o.VerifyCmd != "" {
		return []VerifyContract{{Name: "verify-cmd", Command: o.VerifyCmd, RetryAgent: true}}
	}
	return o.Contracts
}

func (o Options) contractsDir() string {
	if o.VerifyCmd == "" && o.ContractsDir != "" {
		return o.ContractsDir
	}
	return o.RepoPath
}

func Launch(opts Options) (returnErr error) {
	if len(opts.AgentCommand) == 0 {
		return fmt.Errorf("no agent command specified")
	}
	if err := correlation.ValidateTaskRef(opts.TaskRef); err != nil {
		return fmt.Errorf("invalid task reference: %w", err)
	}

	absPath, slug, isSSH, err := ResolveRepo(opts.RepoPath)
	if err != nil {
		return fmt.Errorf("resolve repo: %w", err)
	}

	if isSSH {
		return fmt.Errorf("repository %s uses an SSH remote; managed sessions require HTTPS remotes\n"+
			"Hint: git remote set-url origin https://github.com/%s.git", absPath, slug)
	}

	runID, err := telemetry.NewRunID()
	if err != nil {
		return err
	}
	rec, err := telemetry.StartRun(telemetry.RunContext{
		RunID:           runID,
		TaskRef:         opts.TaskRef,
		AgentName:       opts.AgentName,
		ConfiguredModel: opts.ConfiguredModel,
		Repo:            slug,
		HostRepoPath:    absPath,
		AgentCommand:    opts.AgentCommand,
		VerifyEnabled:   len(opts.verifyContracts()) > 0,
		MaxRetries:      opts.MaxRetries,
		AIAgentVersion:  opts.AIAgentVersion,
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
	resources := []string{"github:repo:" + slug}
	if opts.ObservabilityResource != "" {
		resource, parseErr := api.ParseResourceURI(opts.ObservabilityResource)
		if parseErr != nil || resource.Provider != "langfuse" || resource.Kind != "project" {
			return fmt.Errorf("invalid observability resource %q", opts.ObservabilityResource)
		}
		resources = append(resources, opts.ObservabilityResource)
	}
	client := newBrokerClient(opts.SocketPath)
	resp, err := client.CreateSession(api.CreateSessionRequest{
		AgentName:    opts.AgentName,
		HostRepoPath: absPath,
		Resources:    resources,
		RunID:        runID,
		TaskRef:      opts.TaskRef,
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
		AgentName:  opts.AgentName,
		Repo:       slug,
		SocketPath: opts.SocketPath,
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
		map[string]string{"github": opts.GhWrapper},
		profiles.All(),
	)
	if err != nil {
		revoke()
		return fmt.Errorf("prepare command wrappers: %w", err)
	}
	defer cleanupGh()
	for _, provider := range skippedProviders {
		fmt.Fprintf(os.Stderr, "warning: no command wrapper configured for provider %s; its declared commands are not interposed on PATH\n", provider)
	}

	env := ScrubEnv(
		os.Environ(),
		opts.CredHelper,
		opts.SocketPath,
		resp.SessionID,
		childBindFD,
		slug,
		ghWrapperDir,
		opts.RealGhPath,
	)
	env = append(env, "AI_AGENT_RUN_ID="+runID)
	if opts.TaskRef != "" {
		env = append(env, "AI_AGENT_TASK_REF="+opts.TaskRef)
	}
	if !opts.DisableHomeIsolation {
		homeDir, cleanupHome, homeErr := prepareIsolatedHome(envValue(env, "HOME"))
		if homeErr != nil {
			revoke()
			return fmt.Errorf("prepare isolated run home: %w", homeErr)
		}
		defer cleanupHome()
		env = applyIsolatedHome(env, homeDir)
	}
	agentBin, err := exec.LookPath(opts.AgentCommand[0])
	if err != nil {
		revoke()
		terminalPhase = telemetry.PhaseAgentStart
		return fmt.Errorf("agent binary not found: %w", err)
	}
	var exporter telemetry.OTLPExporter
	if opts.ObservabilityResource != "" {
		exporter = &brokerTelemetryExporter{
			client:     client,
			sessionID:  resp.SessionID,
			bindSecret: resp.BindSecret,
			resource:   opts.ObservabilityResource,
		}
	}
	if nativeTelemetrySupported(opts.AgentCommand) || exporter != nil {
		relay, relayErr := telemetry.StartNativeRelay(rec, exporter)
		if relayErr != nil {
			fmt.Fprintf(os.Stderr, "warning: native telemetry disabled: %v\n", relayErr)
		} else {
			closeRelay = relay.Close
			env = nativeTelemetryEnv(env, opts.AgentCommand, relay, runID)
			opts.AgentCommand = nativeTelemetryCommand(opts.AgentCommand, relay)
		}
	}
	fmt.Fprintf(os.Stderr, "run %s session %s created for %s on %s (expires %s)\n",
		runID, resp.SessionID, opts.AgentName, slug, resp.ExpiresAt.Format("15:04:05"))

	if len(opts.verifyContracts()) > 0 {
		terminalPhase = telemetry.PhaseVerify
		return launchWithVerify(agentBin, opts, env, bindFile, resp.SessionID, revoke, rec)
	}
	terminalPhase = telemetry.PhaseAgent
	return superviseAgent(agentBin, opts, env, bindFile, resp.SessionID, revoke, rec)
}

func superviseAgent(agentBin string, opts Options, env []string, bindFile *os.File, sessionID string, revoke func(), rec *telemetry.Recorder) error {
	agentCmd := newAgentCommand(agentBin, opts, env, bindFile)
	rec.AgentStarted(1)
	start := time.Now()
	if err := agentCmd.Start(); err != nil {
		rec.AgentFinished(1, "start_failed", nil, time.Since(start))
		rec.Finish(telemetry.OutcomeAgentFailed, telemetry.PhaseAgentStart, nil, 0)
		cleanup(sessionID, revoke)
		return fmt.Errorf("start agent: %w", err)
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
	if err != nil {
		rec.Finish(recordAgentFailure(rec, err), telemetry.PhaseAgent, exit, 0)
	} else {
		rec.Finish(telemetry.OutcomePassed, telemetry.PhaseAgent, exit, 0)
	}
	cleanup(sessionID, revoke)
	if err != nil {
		return agentExitError(err)
	}
	return nil
}

func newAgentCommand(agentBin string, opts Options, env []string, bindFile *os.File) *exec.Cmd {
	agentCmd := execCommand(agentBin, opts.AgentCommand[1:]...)
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

func launchWithVerify(agentBin string, opts Options, env []string, bindFile *os.File, sessionID string, revoke func(), rec *telemetry.Recorder) error {
	contracts := opts.verifyContracts()
	maxAttempts := opts.MaxRetries + 1
	if maxAttempts < 1 {
		maxAttempts = 1
	}

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		agentCmd := newAgentCommand(agentBin, opts, env, bindFile)
		rec.AgentStarted(attempt)
		agentStart := time.Now()
		if err := runCommandWithSignals(agentCmd); err != nil {
			exit := exitCodePointer(err)
			rec.AgentFinished(attempt, "failed", exit, time.Since(agentStart))
			rec.Finish(recordAgentFailure(rec, err), telemetry.PhaseAgent, exit, 0)
			cleanup(sessionID, revoke)
			return agentExitError(err)
		}
		rec.AgentFinished(attempt, "passed", intPtr(0), time.Since(agentStart))

		failed, result, err := runContracts(contracts, attempt, maxAttempts, opts, env, bindFile, rec)
		if err != nil {
			rec.Finish(telemetry.OutcomeVerifyFailed, telemetry.PhaseVerify, nil, 0)
			cleanup(sessionID, revoke)
			return err
		}
		if failed == nil {
			fmt.Fprintln(os.Stderr, "verify: passed")
			rec.Finish(telemetry.OutcomePassed, telemetry.PhaseVerify, intPtr(0), 0)
			cleanup(sessionID, revoke)
			return nil
		}

		exit := intPtr(result.ExitCode)
		if result.FailureClass == quality.FailureClassSignal {
			rec.SetSignal(result.Signal)
			rec.Finish(telemetry.OutcomeInterrupted, telemetry.PhaseVerify, exit, 0)
			cleanup(sessionID, revoke)
			return &AgentExitError{
				err:  fmt.Errorf("contract %q interrupted by signal %s", failed.Name, result.Signal),
				code: result.ExitCode,
			}
		}
		if !failed.RetryAgent {
			rec.Finish(telemetry.OutcomeVerifyFailed, telemetry.PhaseVerify, exit, 0)
			cleanup(sessionID, revoke)
			return fmt.Errorf("contract %q failed and declares retry \"never\"", failed.Name)
		}
		if attempt < maxAttempts {
			fmt.Fprintf(os.Stderr, "verify: contract %q failed, re-launching agent (retry %d/%d)\n", failed.Name, attempt, opts.MaxRetries)
		}
	}

	rec.Finish(telemetry.OutcomeVerifyFailed, telemetry.PhaseVerify, nil, 0)
	cleanup(sessionID, revoke)
	return fmt.Errorf("verification failed after %d attempt(s)", maxAttempts)
}

func runContracts(contracts []VerifyContract, attempt int, maxAttempts int, opts Options, env []string, bindFile *os.File, rec *telemetry.Recorder) (*VerifyContract, quality.CheckResult, error) {
	var extraFiles []*os.File
	if bindFile != nil {
		extraFiles = []*os.File{bindFile}
	}
	for i := range contracts {
		contract := &contracts[i]
		fmt.Fprintf(os.Stderr, "verify: contract %q running %q (attempt %d/%d)\n", contract.Name, contract.Command, attempt, maxAttempts)
		rec.VerifyStarted(attempt, contract.Name, contract.Command)
		result, checkErr := quality.RunCheck(quality.CheckOptions{
			Command:     []string{"sh", "-c", contract.Command},
			Dir:         opts.contractsDir(),
			Env:         env,
			Stdin:       os.Stdin,
			ExtraFiles:  extraFiles,
			EvidenceDir: filepath.Join(paths.ConfigDir(), "evidence"),
			TailLines:   60,
			Run:         runCommandWithSignals,
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

func printRunSummary(summary telemetry.RunSummary) {
	if summary.RunID == "" || summary.Outcome == "" {
		return
	}
	fmt.Fprintf(os.Stderr, "run %s: %s during %s (%s)\n",
		telemetry.ShortRunID(summary.RunID), summary.Outcome, summary.TerminalPhase,
		time.Duration(summary.DurationMS)*time.Millisecond)
	fmt.Fprintf(os.Stderr, "inspect: ai-agent runs show %s\n", summary.RunID)
}

func prepareCommandWrappers(wrapperByProvider map[string]string, profs []interception.Profile) (dir string, skipped []string, cleanup func(), err error) {
	noop := func() {}
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
