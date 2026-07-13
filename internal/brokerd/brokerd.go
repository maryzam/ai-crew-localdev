package brokerd

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/broker/core"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/governance"
	"github.com/maryzam/ai-crew-localdev/internal/governance/policycheck"
	"github.com/maryzam/ai-crew-localdev/internal/platform/paths"
	"github.com/maryzam/ai-crew-localdev/internal/platform/securefile"
	"github.com/maryzam/ai-crew-localdev/internal/providers/catalog"
)

func Run() error {
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("load broker configuration: %w", err)
	}
	governancePaths := governance.Paths{Identities: cfg.IdentitiesPath, Policy: cfg.PolicyPath}
	snapshot, err := governance.FileStore{}.Load(governancePaths)
	if err != nil {
		return fmt.Errorf("load governance configuration: %w", err)
	}
	if snapshot.IdentitiesError != nil {
		return fmt.Errorf("load governance configuration: %w", snapshot.IdentitiesError)
	}
	if snapshot.PolicyError != nil {
		return fmt.Errorf("load governance configuration: %w", snapshot.PolicyError)
	}
	idents, pol := snapshot.Identities, snapshot.Policy
	if err := policycheck.Validate(pol, idents); err != nil {
		return fmt.Errorf("validate policy: %w", err)
	}

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

	audit, err := core.NewFileAuditLogger(cfg.AuditLogPath)
	if err != nil {
		return fmt.Errorf("create audit logger: %w", err)
	}
	defer func() { _ = audit.Close() }()

	enforcer := core.NewPolicyEnforcer(pol)
	providers, err := catalog.Providers(idents, os.Getenv(paths.EnvGitHubBaseURL))
	if err != nil {
		return fmt.Errorf("construct providers: %w", err)
	}
	b, err := core.NewBroker(cfg, enforcer, audit, providers)
	if err != nil {
		return fmt.Errorf("create broker: %w", err)
	}

	ln, err := getListener(cfg.SocketPath)
	if err != nil {
		return fmt.Errorf("listener: %w", err)
	}
	defer func() { _ = ln.Close() }()

	log.Printf("ai-agent-broker: listening on %s", cfg.SocketPath)

	pidPath := filepath.Join(paths.RuntimeDir(), "broker.pid")
	if err := writePIDFile(pidPath); err != nil {
		log.Printf("warning: could not write PID file: %v", err)
	}
	defer func() { _ = os.Remove(pidPath) }()

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

func loadConfig() (core.BrokerConfig, error) {
	socketPath, err := paths.BrokerListenSocketPath()
	if err != nil {
		return core.BrokerConfig{}, err
	}

	governancePaths := governance.DefaultPaths()

	auditLogPath := paths.AuditLogPath()

	cfg := core.BrokerConfig{
		SocketPath:     socketPath,
		IdentitiesPath: governancePaths.Identities,
		PolicyPath:     governancePaths.Policy,
		AuditLogPath:   auditLogPath,
	}

	if v := os.Getenv(paths.EnvSessionTTL); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.SessionTTL = d
		}
	}
	if v := os.Getenv(paths.EnvIdleTimeout); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.IdleTimeout = d
		}
	}

	return cfg, nil
}

func getListener(socketPath string) (net.Listener, error) {

	if nfds := os.Getenv("LISTEN_FDS"); nfds == "1" {
		pidStr := os.Getenv("LISTEN_PID")
		pid, err := strconv.Atoi(pidStr)
		if err == nil && pid == os.Getpid() {

			f := os.NewFile(3, "systemd-socket")
			ln, err := net.FileListener(f)
			_ = f.Close()
			if err != nil {
				return nil, fmt.Errorf("systemd socket activation: %w", err)
			}
			if err := validateActivatedSocket(ln.Addr(), socketPath); err != nil {
				_ = ln.Close()
				return nil, err
			}
			log.Printf("ai-agent-broker: using systemd-activated socket")
			return ln, nil
		}
	}

	dir := filepath.Dir(socketPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("create socket directory %s: %w", dir, err)
	}

	_ = os.Remove(socketPath)

	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w", socketPath, err)
	}

	if err := os.Chmod(socketPath, 0600); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("chmod socket: %w", err)
	}

	return ln, nil
}

func validateActivatedSocket(addr net.Addr, configured string) error {
	actual := strings.TrimSpace(addr.String())
	if actual != "" && filepath.Clean(actual) == filepath.Clean(configured) {
		return nil
	}
	return fmt.Errorf("systemd-activated socket %q does not match the configured broker socket %q; update ListenStream in ai-agent-broker.socket (re-run 'ai-agent install') or unset %s", actual, configured, paths.EnvBrokerSocket)
}

func writePIDFile(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	return securefile.WriteOwnerOnly(path, []byte(strconv.Itoa(os.Getpid())))
}
