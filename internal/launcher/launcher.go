package launcher

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"

	"github.com/maryzam/ai-crew-localdev/internal/broker"
	"github.com/maryzam/ai-crew-localdev/internal/brokerclient"
)

// Options configures the session launch.
type Options struct {
	AgentName    string
	RepoPath     string // local filesystem path (default: cwd)
	SocketPath   string // broker socket path
	CredHelper   string // path to ai-agent-credential-helper binary
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
	client := &brokerclient.Client{SocketPath: opts.SocketPath}
	resp, err := client.CreateSession(broker.CreateSessionRequest{
		AgentName:    opts.AgentName,
		Repo:         slug,
		HostRepoPath: absPath,
	})
	if err != nil {
		return fmt.Errorf("create session: %w", err)
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
		return fmt.Errorf("create bind FD: %w", err)
	}

	// 5. Build scrubbed environment.
	env := ScrubEnv(
		os.Environ(),
		opts.CredHelper,
		opts.SocketPath,
		resp.SessionID,
		bindFD,
	)

	// 6. Resolve agent binary.
	if len(opts.AgentCommand) == 0 {
		return fmt.Errorf("no agent command specified")
	}
	agentBin, err := exec.LookPath(opts.AgentCommand[0])
	if err != nil {
		return fmt.Errorf("agent binary not found: %w", err)
	}

	// 7. Report session info.
	fmt.Fprintf(os.Stderr, "session %s created for %s on %s (expires %s)\n",
		resp.SessionID, opts.AgentName, slug, resp.ExpiresAt.Format("15:04:05"))

	// 8. Exec the agent process, inheriting the bind FD.
	//
	// syscall.Exec replaces the current process. The bind FD is inherited
	// because we do not set CloseOnExec. The child reads it via
	// /proc/self/fd/$AI_AGENT_SESSION_BIND_FD.
	return syscall.Exec(agentBin, opts.AgentCommand, env)
}
