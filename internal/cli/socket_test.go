package cli

import "testing"

func TestResolveBrokerSocketPathPrefersFlag(t *testing.T) {
	t.Setenv("AI_AGENT_AUTH_SOCK", "/run/ai-agent/broker.sock")

	got := resolveBrokerSocketPath("/tmp/custom.sock")
	if got != "/tmp/custom.sock" {
		t.Fatalf("resolveBrokerSocketPath = %q, want %q", got, "/tmp/custom.sock")
	}
}

func TestResolveBrokerSocketPathUsesEnvWhenSet(t *testing.T) {
	t.Setenv("AI_AGENT_AUTH_SOCK", "/run/ai-agent/broker.sock")

	got := resolveBrokerSocketPath("")
	if got != "/run/ai-agent/broker.sock" {
		t.Fatalf("resolveBrokerSocketPath = %q, want %q", got, "/run/ai-agent/broker.sock")
	}
}

func TestResolveBrokerSocketPathFallsBackToDefault(t *testing.T) {
	t.Setenv("AI_AGENT_AUTH_SOCK", "")

	got := resolveBrokerSocketPath("")
	if got == "" {
		t.Fatal("resolveBrokerSocketPath returned empty string")
	}
}
