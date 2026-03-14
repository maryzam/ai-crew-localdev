package launcher

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"github.com/maryzam/ai-crew-localdev/internal/broker"
	"github.com/maryzam/ai-crew-localdev/internal/brokerclient"
)

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
}

// Launch creates a broker session and execs the agent CLI with fail-closed
// environment and memfd-based bind secret delivery.
func Launch(opts Options) error {
	// 1. Resolve repository.
	absPath, slug, isSSH, err := ResolveRepo(opts.RepoPath)
	if err != nil {
		return fmt.Errorf("resolve repo: %w", err)
	}

	// Phase 1: HTTPS-only managed sessions (LDR-002).
	if isSSH {
		return fmt.Errorf("repository %s uses an SSH remote; managed sessions require HTTPS remotes\n"+
			"Hint: git remote set-url origin https://github.com/%s.git", absPath, slug)
	}

	// 2. Create broker session.
	client := newBrokerClient(opts.SocketPath)
	resp, err := client.CreateSession(broker.CreateSessionRequest{
		AgentName:    opts.AgentName,
		Repo:         slug,
		HostRepoPath: absPath,
	})
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}

	revoke := func() {
		_ = client.RevokeSession(broker.RevokeSessionRequest{
			SessionID:  resp.SessionID,
			BindSecret: resp.BindSecret,
		})
	}

	// 3. Save session info for later use by revoke/status commands.
	if err := SaveSessionInfo(SessionInfo{
		SessionID:  resp.SessionID,
		BindSecret: resp.BindSecret,
		AgentName:  opts.AgentName,
		Repo:       slug,
		SocketPath: opts.SocketPath,
	}); err != nil {
		// Non-fatal: session still works, just can't be managed via CLI.
		fmt.Fprintf(os.Stderr, "warning: could not save session info: %v\n", err)
	}

	// 4. Create memfd with bind secret.
	bindFD, err := CreateBindFD(resp.BindSecret)
	if err != nil {
		revoke()
		return fmt.Errorf("create bind FD: %w", err)
	}

	// 5. Prepare gh wrapper PATH override.
	ghWrapperDir, cleanupGh, err := prepareGhWrapper(opts.GhWrapper)
	if err != nil {
		revoke()
		return fmt.Errorf("prepare gh wrapper: %w", err)
	}
	defer cleanupGh()

	// 6. Build scrubbed environment.
	env := ScrubEnv(
		os.Environ(),
		opts.CredHelper,
		opts.SocketPath,
		resp.SessionID,
		bindFD,
		slug,
		ghWrapperDir,
		opts.RealGhPath,
	)

	// 7. Resolve agent binary.
	if len(opts.AgentCommand) == 0 {
		revoke()
		return fmt.Errorf("no agent command specified")
	}
	agentBin, err := exec.LookPath(opts.AgentCommand[0])
	if err != nil {
		revoke()
		return fmt.Errorf("agent binary not found: %w", err)
	}

	// 8. Report session info.
	fmt.Fprintf(os.Stderr, "session %s created for %s on %s (expires %s)\n",
		resp.SessionID, opts.AgentName, slug, resp.ExpiresAt.Format("15:04:05"))

	// 9. Exec the agent process, inheriting the bind FD.
	//
	// syscall.Exec replaces the current process. The bind FD is inherited
	// because we do not set CloseOnExec. The child reads it via
	// /proc/self/fd/$AI_AGENT_SESSION_BIND_FD.
	return syscall.Exec(agentBin, opts.AgentCommand, env)
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
		os.RemoveAll(dir)
		return "", noop, fmt.Errorf("create gh symlink: %w", err)
	}

	return dir, func() { os.RemoveAll(dir) }, nil
}
