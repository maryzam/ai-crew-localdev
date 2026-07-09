package cli

import (
	"strings"
	"testing"

	"github.com/maryzam/ai-crew-localdev/internal/platform/paths"
)

func TestResolveBrokerSocketPathPrefersFlag(t *testing.T) {
	t.Setenv("AI_AGENT_AUTH_SOCK", "relative.sock")

	got, err := resolveBrokerSocketPath("/tmp/custom.sock")
	if err != nil {
		t.Fatalf("resolveBrokerSocketPath returned error: %v", err)
	}
	if got != "/tmp/custom.sock" {
		t.Fatalf("resolveBrokerSocketPath = %q, want %q", got, "/tmp/custom.sock")
	}
}

func TestResolveBrokerSocketPathUsesEnvWhenSet(t *testing.T) {
	t.Setenv("AI_AGENT_AUTH_SOCK", "/run/ai-agent/broker.sock")

	got, err := resolveBrokerSocketPath("")
	if err != nil {
		t.Fatalf("resolveBrokerSocketPath returned error: %v", err)
	}
	if got != "/run/ai-agent/broker.sock" {
		t.Fatalf("resolveBrokerSocketPath = %q, want %q", got, "/run/ai-agent/broker.sock")
	}
}

func TestResolveBrokerSocketPathFallsBackToDefault(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/tmp/ai-agent-runtime")
	t.Setenv("AI_AGENT_AUTH_SOCK", "")

	got, err := resolveBrokerSocketPath("")
	if err != nil {
		t.Fatalf("resolveBrokerSocketPath returned error: %v", err)
	}
	if got != paths.DefaultSocketPath() {
		t.Fatalf("resolveBrokerSocketPath = %q, want %q", got, paths.DefaultSocketPath())
	}
}

func TestResolveBrokerSocketPathTreatsWhitespaceOnlyEnvAsUnset(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "/tmp/ai-agent-runtime")
	t.Setenv("AI_AGENT_AUTH_SOCK", "   \t")

	got, err := resolveBrokerSocketPath("")
	if err != nil {
		t.Fatalf("resolveBrokerSocketPath returned error: %v", err)
	}
	if got != paths.DefaultSocketPath() {
		t.Fatalf("resolveBrokerSocketPath = %q, want %q", got, paths.DefaultSocketPath())
	}
}

func TestResolveBrokerSocketPathRejectsRelativeEnv(t *testing.T) {
	t.Setenv("AI_AGENT_AUTH_SOCK", "broker.sock")

	_, err := resolveBrokerSocketPath("")
	if err == nil {
		t.Fatal("expected error for relative AI_AGENT_AUTH_SOCK")
	}
	if !strings.Contains(err.Error(), "absolute path") {
		t.Fatalf("error = %q, want absolute path message", err)
	}
}

func TestResolveBrokerSocketPathCleansEnvValue(t *testing.T) {
	t.Setenv("AI_AGENT_AUTH_SOCK", "/run/ai-agent//broker.sock")

	got, err := resolveBrokerSocketPath("")
	if err != nil {
		t.Fatalf("resolveBrokerSocketPath returned error: %v", err)
	}
	if got != "/run/ai-agent/broker.sock" {
		t.Fatalf("resolveBrokerSocketPath = %q, want %q", got, "/run/ai-agent/broker.sock")
	}
}

func TestResolveSessionBrokerSocketPathRejectsRelativeStoredPath(t *testing.T) {
	_, err := resolveSessionBrokerSocketPath("", "relative.sock")
	if err == nil {
		t.Fatal("expected error for relative stored session socket path")
	}
	if !strings.Contains(err.Error(), "session file socket path") {
		t.Fatalf("error = %q, want session file socket path message", err)
	}
}

func TestResolveSessionBrokerSocketPathUsesStoredPathWhenValid(t *testing.T) {
	got, err := resolveSessionBrokerSocketPath("", "/run/ai-agent//broker.sock")
	if err != nil {
		t.Fatalf("resolveSessionBrokerSocketPath returned error: %v", err)
	}
	if got != "/run/ai-agent/broker.sock" {
		t.Fatalf("resolveSessionBrokerSocketPath = %q, want %q", got, "/run/ai-agent/broker.sock")
	}
}

func TestResolveBrokerSocketPathFollowsTheDaemonEnv(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	t.Setenv(paths.EnvAuthSock, "")
	t.Setenv(paths.EnvBrokerSocket, "/custom/broker.sock")

	got, err := resolveBrokerSocketPath("")
	if err != nil {
		t.Fatalf("resolveBrokerSocketPath: %v", err)
	}
	if got != "/custom/broker.sock" {
		t.Fatalf("client resolved %q; an operator who points the daemon at %s must get a CLI that follows it", got, paths.EnvBrokerSocket)
	}

	t.Setenv(paths.EnvAuthSock, "/session/broker.sock")
	got, err = resolveBrokerSocketPath("")
	if err != nil {
		t.Fatalf("resolveBrokerSocketPath: %v", err)
	}
	if got != "/session/broker.sock" {
		t.Fatalf("client resolved %q; the session env must win over the daemon env", got)
	}
}
