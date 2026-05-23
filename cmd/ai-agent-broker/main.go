// ai-agent-broker is the host broker daemon for the ai-agent authentication
// architecture. It listens on a Unix domain socket, loads GitHub App private
// keys into memory, signs JWTs, and mints scoped installation access tokens
// on behalf of authenticated agent sessions.
//
// The broker supports systemd socket activation: if LISTEN_FDS=1 and
// LISTEN_PID match, it uses the inherited file descriptor as the listener.
// Otherwise, it creates its own socket.
//
// Signals:
//   - SIGHUP: reload policy file
//   - SIGTERM/SIGINT: graceful shutdown
package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/broker"
	ghprov "github.com/maryzam/ai-crew-localdev/internal/broker/providers/github"
	"github.com/maryzam/ai-crew-localdev/internal/config"
	"github.com/maryzam/ai-crew-localdev/internal/identity"
	"github.com/maryzam/ai-crew-localdev/internal/policy"
)

func main() {
	if err := run(); err != nil {
		log.Fatalf("ai-agent-broker: %v", err)
	}
}

func run() error {
	cfg := loadConfig()

	// Load identities.
	idents, err := identity.Load(config.DefaultIdentitiesPath())
	if err != nil {
		return fmt.Errorf("load identities: %w", err)
	}

	// Load policy.
	policyData, err := os.ReadFile(cfg.PolicyPath)
	if err != nil {
		return fmt.Errorf("read policy: %w", err)
	}
	pol, err := policy.ParsePolicy(policyData)
	if err != nil {
		return fmt.Errorf("parse policy: %w", err)
	}
	if result := policy.Validate(pol); result.Errors.HasErrors() {
		return fmt.Errorf("validate policy: %s", result.Errors.Error())
	}

	// Apply policy TTL defaults when not overridden by env vars.
	if cfg.SessionTTL == 0 && pol.DefaultSessionTTL != "" {
		if d, err := time.ParseDuration(pol.DefaultSessionTTL); err == nil {
			cfg.SessionTTL = d
		}
	}
	if cfg.IdleTimeout == 0 && pol.DefaultIdleTimeout != "" {
		if d, err := time.ParseDuration(pol.DefaultIdleTimeout); err == nil {
			cfg.IdleTimeout = d
		}
	}

	// Load PEM keys and create signer.
	signer, err := broker.NewSigner(idents)
	if err != nil {
		return fmt.Errorf("create signer: %w", err)
	}

	// Create audit logger.
	audit, err := broker.NewFileAuditLogger(cfg.AuditLogPath)
	if err != nil {
		return fmt.Errorf("create audit logger: %w", err)
	}
	defer func() { _ = audit.Close() }()

	enforcer := broker.NewPolicyEnforcer(pol, "github")
	githubBaseURL := os.Getenv("AI_AGENT_GITHUB_BASE_URL")
	githubProvider := ghprov.New(
		broker.NewGitHubClient(githubBaseURL),
		signer,
		appIDResolver(idents),
	)
	b, err := broker.NewBroker(cfg, enforcer, audit, []broker.CredentialProvider{githubProvider})
	if err != nil {
		return fmt.Errorf("create broker: %w", err)
	}

	// Obtain listener: systemd socket activation or create our own.
	ln, err := getListener(cfg.SocketPath)
	if err != nil {
		return fmt.Errorf("listener: %w", err)
	}
	defer func() { _ = ln.Close() }()

	log.Printf("ai-agent-broker: listening on %s", cfg.SocketPath)

	// Write PID file for reload commands.
	pidPath := filepath.Join(config.RuntimeDir(), "broker.pid")
	if err := writePIDFile(pidPath); err != nil {
		log.Printf("warning: could not write PID file: %v", err)
	}
	defer func() { _ = os.Remove(pidPath) }()

	// Set up signal handling.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGHUP, syscall.SIGTERM, syscall.SIGINT)

	go func() {
		for sig := range sigCh {
			switch sig {
			case syscall.SIGHUP:
				log.Printf("ai-agent-broker: reloading policy from %s", cfg.PolicyPath)
				if err := b.ReloadPolicy(); err != nil {
					log.Printf("ai-agent-broker: policy reload failed: %v", err)
				} else {
					log.Printf("ai-agent-broker: policy reloaded successfully")
				}
			case syscall.SIGTERM, syscall.SIGINT:
				log.Printf("ai-agent-broker: shutting down")
				cancel()
				_ = ln.Close()
			}
		}
	}()

	return b.Serve(ctx, ln)
}

func loadConfig() broker.BrokerConfig {
	socketPath := os.Getenv("AI_AGENT_BROKER_SOCKET")
	if socketPath == "" {
		socketPath = filepath.Join(config.RuntimeDir(), "broker.sock")
	}

	policyPath := os.Getenv("AI_AGENT_POLICY_PATH")
	if policyPath == "" {
		policyPath = config.DefaultPolicyPath()
	}

	auditLogPath := os.Getenv("AI_AGENT_AUDIT_LOG")
	if auditLogPath == "" {
		auditLogPath = filepath.Join(config.ConfigDir(), "audit.log")
	}

	cfg := broker.BrokerConfig{
		SocketPath:   socketPath,
		PolicyPath:   policyPath,
		AuditLogPath: auditLogPath,
	}

	if v := os.Getenv("AI_AGENT_SESSION_TTL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.SessionTTL = d
		}
	}
	if v := os.Getenv("AI_AGENT_IDLE_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.IdleTimeout = d
		}
	}

	return cfg
}

// getListener returns a systemd-activated listener if available, or creates
// a new Unix domain socket.
func getListener(socketPath string) (net.Listener, error) {
	// Check for systemd socket activation (sd_listen_fds protocol).
	if nfds := os.Getenv("LISTEN_FDS"); nfds == "1" {
		pidStr := os.Getenv("LISTEN_PID")
		pid, err := strconv.Atoi(pidStr)
		if err == nil && pid == os.Getpid() {
			// FD 3 is the first passed socket.
			f := os.NewFile(3, "systemd-socket")
			ln, err := net.FileListener(f)
			_ = f.Close()
			if err != nil {
				return nil, fmt.Errorf("systemd socket activation: %w", err)
			}
			log.Printf("ai-agent-broker: using systemd-activated socket")
			return ln, nil
		}
	}

	// Create our own socket.
	dir := filepath.Dir(socketPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create socket directory %s: %w", dir, err)
	}

	// Remove stale socket.
	_ = os.Remove(socketPath)

	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", socketPath, err)
	}

	// Set socket permissions to owner-only.
	if err := os.Chmod(socketPath, 0600); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("chmod socket: %w", err)
	}

	return ln, nil
}

func appIDResolver(idents *identity.IdentitiesFile) func(string) string {
	return func(agent string) string {
		if a, ok := idents.Agents[agent]; ok {
			return a.AppID
		}
		return ""
	}
}

func writePIDFile(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())), 0600)
}
