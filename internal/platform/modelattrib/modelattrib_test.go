package modelattrib

import (
	"strings"
	"testing"
)

func TestResolveUsesProfileMetadataAndSharedCommandName(t *testing.T) {
	for _, key := range []string{"AI_AGENT_MODEL", "ACME_MODEL", "OPENAI_MODEL", "ANTHROPIC_MODEL", "GEMINI_MODEL"} {
		t.Setenv(key, "")
	}
	t.Setenv("ACME_MODEL", strings.Repeat("x", MaxPropagatedValueLength+10))

	profiles := []AgentProfile{{
		Name:     "acme",
		Type:     "acme_agent",
		Provider: "fake-ai",
		Family:   "fake-family",
		Tools:    []string{"AcmeAgent"},
		ModelEnv: []string{"ACME_MODEL"},
	}}

	agent, model := Resolve("custom", "", []string{"/opt/tools/AcmeAgent.EXE"}, profiles)
	if agent.Type != "acme_agent" || agent.Command != "AcmeAgent.EXE" {
		t.Fatalf("agent = %#v", agent)
	}
	if model.Provider != "fake-ai" || model.Family != "fake-family" {
		t.Fatalf("model = %#v", model)
	}
	if len(model.Requested) != MaxPropagatedValueLength || model.Resolution.PrimarySource != "environment" {
		t.Fatalf("resolution model = %#v", model)
	}
}

func TestResolveRetainsStandardProfilesForFallbacks(t *testing.T) {
	for _, key := range []string{"AI_AGENT_MODEL", "CODEX_MODEL", "OPENAI_MODEL", "CLAUDE_MODEL", "ANTHROPIC_MODEL", "GEMINI_MODEL"} {
		t.Setenv(key, "")
	}

	agent, model := Resolve("gemini-reviewer", "", []string{"gemini", "--model", "gemini-2.5-pro"}, nil)
	if agent.Type != "gemini" || model.Provider != "gcp.gemini" || model.Family != "gemini-2.5" {
		t.Fatalf("agent = %#v model = %#v", agent, model)
	}
}
