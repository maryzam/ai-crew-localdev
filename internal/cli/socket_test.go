package cli

import (
	"strings"
	"testing"

	"github.com/maryzam/ai-crew-localdev/internal/config"
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
	if got != config.DefaultSocketPath() {
		t.Fatalf("resolveBrokerSocketPath = %q, want %q", got, config.DefaultSocketPath())
	}
}

func TestResolveBrokerSocketPathRejectsWhitespaceOnlyEnv(t *testing.T) {
	t.Setenv("AI_AGENT_AUTH_SOCK", "   \t")

	_, err := resolveBrokerSocketPath("")
	if err == nil {
		t.Fatal("expected error for whitespace-only AI_AGENT_AUTH_SOCK")
	}
	if !strings.Contains(err.Error(), "whitespace-only") {
		t.Fatalf("error = %q, want whitespace-only message", err)
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
