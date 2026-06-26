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

	"github.com/maryzam/ai-crew-localdev/internal/broker"
	"github.com/maryzam/ai-crew-localdev/internal/brokerclient"
	"github.com/maryzam/ai-crew-localdev/internal/telemetry"
)

// execCommand is a test seam for os/exec.Command.
var execCommand = exec.Command

const childBindFD = 3

// AgentExitError reports the agent's own exit status after the launcher has
// reclaimed control and cleaned up the broker session.
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
	CreateSession(broker.CreateSessionRequest) (*broker.CreateSessionResponse, error)
	RevokeSession(broker.RevokeSessionRequest) error
}

var newBrokerClient = func(socketPath string) brokerClient {
	return &brokerclient.Client{SocketPath: socketPath}
}

// Options configures the session launch.
type Options struct {
	AgentName    string
	RepoPath     string // local filesystem path (default: cwd)
	SocketPath   string // broker socket path
	CredHelper   string // path to ai-agent-credential-helper binary
	GhWrapper    string // path to ai-agent-gh binary
	RealGhPath   string // path to real gh binary preserved through the shim
	AgentCommand []string

	// VerifyCmd, when non-empty, enables the verify-and-retry loop.
	// After the agent exits successfully, this command is executed via "sh -c".
	// If it fails, the agent is re-launched up to MaxRetries times.
	VerifyCmd  string
	MaxRetries int
}

// Launch creates a broker session and execs the agent CLI with fail-closed
// environment and memfd-based bind secret delivery.
func Launch(opts Options) error {
	if len(opts.AgentCommand) == 0 {
		return fmt.Errorf("no agent command specified")
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
		RunID:         runID,
		AgentName:     opts.AgentName,
		Repo:          slug,
		HostRepoPath:  absPath,
		AgentCommand:  opts.AgentCommand,
		VerifyEnabled: opts.VerifyCmd != "",
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: managed-run telemetry disabled: %v\n", err)
	}
	if rec != nil {
		defer func() { _ = rec.Close() }()
	}

	client := newBrokerClient(opts.SocketPath)
	resp, err := client.CreateSession(broker.CreateSessionRequest{
		AgentName:    opts.AgentName,
		HostRepoPath: absPath,
		Resources:    []string{"github:repo:" + slug},
		RunID:        runID,
	})
	if err != nil {
		if rec != nil {
			rec.Finished("session_create_failed", nil, 0, 0)
		}
		return fmt.Errorf("create session: %w", err)
	}
	if rec != nil {
		rec.SetSessionID(resp.SessionID)
	}

	revoke := func() {
		_ = client.RevokeSession(broker.RevokeSessionRequest{
			SessionID:  resp.SessionID,
			BindSecret: resp.BindSecret,
		})
	}

	if err := SaveSessionInfo(SessionInfo{
		SessionID:  resp.SessionID,
		AgentName:  opts.AgentName,
		Repo:       slug,
		SocketPath: opts.SocketPath,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not save session info: %v\n", err)
	}

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

	ghWrapperDir, cleanupGh, err := prepareGhWrapper(opts.GhWrapper)
	if err != nil {
		revoke()
		return fmt.Errorf("prepare gh wrapper: %w", err)
	}
	defer cleanupGh()

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
	agentBin, err := exec.LookPath(opts.AgentCommand[0])
	if err != nil {
		revoke()
		if rec != nil {
			rec.Finished("agent_not_found", nil, 0, 0)
		}
		return fmt.Errorf("agent binary not found: %w", err)
	}

	fmt.Fprintf(os.Stderr, "run %s session %s created for %s on %s (expires %s)\n",
		runID, resp.SessionID, opts.AgentName, slug, resp.ExpiresAt.Format("15:04:05"))

	if opts.VerifyCmd != "" {
		return launchWithVerify(agentBin, opts, env, bindFile, resp.SessionID, revoke, rec)
	}
	return superviseAgent(agentBin, opts, env, bindFile, resp.SessionID, revoke, rec)
}

// superviseAgent runs the agent as a child and revokes the session when it
// exits, so a session never outlives its agent.
func superviseAgent(agentBin string, opts Options, env []string, bindFile *os.File, sessionID string, revoke func(), rec *telemetry.Recorder) error {
	agentCmd := newAgentCommand(agentBin, opts, env, bindFile)
	if rec != nil {
		rec.AgentStarted(1)
	}
	start := time.Now()
	if err := agentCmd.Start(); err != nil {
		cleanup(sessionID, revoke)
		if rec != nil {
			rec.AgentFinished(1, "start_failed", nil, time.Since(start))
			rec.Finished("agent_start_failed", nil, 0, 0)
		}
		return fmt.Errorf("start agent: %w", err)
	}

	stopForwarding := forwardSignals(agentCmd)
	defer stopForwarding()

	err := agentCmd.Wait()
	exit := exitCodePointer(err)
	if rec != nil {
		if err != nil {
			rec.AgentFinished(1, "failed", exit, time.Since(start))
			rec.UsageUnknown()
			rec.Finished("agent_failed", exit, 0, 0)
		} else {
			rec.AgentFinished(1, "passed", exit, time.Since(start))
			rec.UsageUnknown()
			rec.Finished("passed", exit, 0, 0)
		}
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

// attachBindFile maps the session bind file to fd 3 in the child process.
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

// forwardSignals relays termination signals to the agent and keeps the
// launcher alive until the agent exits, so revocation always runs.
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

// cleanup revokes the broker session and removes the local session file.
func cleanup(sessionID string, revoke func()) {
	revoke()
	_ = RemoveSessionInfo(sessionID)
}

// launchWithVerify runs the agent as a subprocess and, on successful exit,
// executes the verify command. If verification fails the agent is re-launched
// up to MaxRetries times. The session is cleaned up on every exit path.
func launchWithVerify(agentBin string, opts Options, env []string, bindFile *os.File, sessionID string, revoke func(), rec *telemetry.Recorder) error {
	maxAttempts := opts.MaxRetries + 1
	if maxAttempts < 1 {
		maxAttempts = 1
	}

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		agentCmd := newAgentCommand(agentBin, opts, env, bindFile)
		if rec != nil {
			rec.AgentStarted(attempt)
		}
		agentStart := time.Now()
		if err := agentCmd.Run(); err != nil {
			exit := exitCodePointer(err)
			if rec != nil {
				rec.AgentFinished(attempt, "failed", exit, time.Since(agentStart))
				rec.UsageUnknown()
				rec.Finished("agent_failed", exit, attempt-1, 0)
			}
			cleanup(sessionID, revoke)
			return agentExitError(err)
		}
		if rec != nil {
			rec.AgentFinished(attempt, "passed", intPtr(0), time.Since(agentStart))
		}

		fmt.Fprintf(os.Stderr, "verify: running %q (attempt %d/%d)\n", opts.VerifyCmd, attempt, maxAttempts)
		verifyCmd := execCommand("sh", "-c", opts.VerifyCmd)
		verifyCmd.Env = env
		verifyCmd.Dir = opts.RepoPath
		verifyCmd.Stdout = os.Stderr
		verifyCmd.Stderr = os.Stderr
		attachBindFile(verifyCmd, bindFile)

		if rec != nil {
			rec.VerifyStarted(attempt, opts.VerifyCmd)
		}
		verifyStart := time.Now()
		if err := verifyCmd.Run(); err == nil {
			fmt.Fprintln(os.Stderr, "verify: passed")
			if rec != nil {
				rec.VerifyFinished(attempt, "passed", intPtr(0), time.Since(verifyStart))
				rec.UsageUnknown()
				rec.Finished("passed", intPtr(0), attempt-1, 0)
			}
			cleanup(sessionID, revoke)
			return nil
		} else if rec != nil {
			rec.VerifyFinished(attempt, "failed", exitCodePointer(err), time.Since(verifyStart))
		}

		if attempt < maxAttempts {
			fmt.Fprintf(os.Stderr, "verify: failed, re-launching agent (retry %d/%d)\n", attempt, opts.MaxRetries)
		}
	}

	cleanup(sessionID, revoke)
	if rec != nil {
		rec.UsageUnknown()
		rec.Finished("verify_failed", nil, maxAttempts-1, 0)
	}
	return fmt.Errorf("verify command %q failed after %d attempt(s)", opts.VerifyCmd, maxAttempts)
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

func intPtr(v int) *int {
	return &v
}

// prepareGhWrapper creates a temporary directory containing a "gh" symlink
// that points to the ai-agent-gh wrapper binary. The directory is intended to
// be prepended to PATH so plain gh invocations route through the wrapper.
func prepareGhWrapper(ghWrapperPath string) (dir string, cleanup func(), err error) {
	noop := func() {}
	if ghWrapperPath == "" {
		return "", noop, nil
	}

	absWrapper, err := filepath.Abs(ghWrapperPath)
	if err != nil {
		return "", noop, fmt.Errorf("resolve gh wrapper path: %w", err)
	}

	if _, err := os.Stat(absWrapper); err != nil {
		return "", noop, fmt.Errorf("gh wrapper not found at %s: %w", absWrapper, err)
	}

	dir, err = os.MkdirTemp("", "ai-agent-gh-shim-*")
	if err != nil {
		return "", noop, fmt.Errorf("create gh wrapper dir: %w", err)
	}

	ghLink := filepath.Join(dir, "gh")
	if err := os.Symlink(absWrapper, ghLink); err != nil {
		_ = os.RemoveAll(dir)
		return "", noop, fmt.Errorf("create gh symlink: %w", err)
	}

	return dir, func() { _ = os.RemoveAll(dir) }, nil
}
