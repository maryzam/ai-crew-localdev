package telemetry

import "testing"

func TestResolveAgentModelUsesMultipleSignalsAndFallbacks(t *testing.T) {
	for _, key := range []string{"AI_AGENT_MODEL", "CODEX_MODEL", "OPENAI_MODEL", "CLAUDE_MODEL", "ANTHROPIC_MODEL", "GEMINI_MODEL"} {
		t.Setenv(key, "")
	}

	t.Run("secondary agent signal", func(t *testing.T) {
		agent, model := ResolveAgentModel("claude-reviewer", []string{"claude"})
		if agent.Type != "claude_code" || model.Provider != "anthropic" || model.Family != "claude" {
			t.Fatalf("fallback attribution = agent %#v model %#v", agent, model)
		}
		if model.Resolution.Status != "partial" || model.Resolution.Confidence != "inferred" {
			t.Fatalf("resolution = %#v", model.Resolution)
		}
	})

	t.Run("CLI wins and conflict is retained", func(t *testing.T) {
		t.Setenv("OPENAI_MODEL", "gpt-4.1")
		_, model := ResolveAgentModel("codex", []string{"codex", "--model", "gpt-5.2-codex"})
		if model.Requested != "gpt-5.2-codex" || model.Family != "gpt-5" {
			t.Fatalf("model = %#v", model)
		}
		if !model.Resolution.Conflict || len(model.Resolution.Sources) != 2 {
			t.Fatalf("resolution = %#v", model.Resolution)
		}
	})

	t.Run("unknown executable is honestly unresolved", func(t *testing.T) {
		_, model := ResolveAgentModel("custom", []string{"custom-agent"})
		if model.Resolution.Status != "unresolved" || model.Provider != "" || model.Family != "" {
			t.Fatalf("model = %#v", model)
		}
	})

	t.Run("versioned Claude model keeps family", func(t *testing.T) {
		_, model := ResolveAgentModel("claude", []string{"claude", "--model", "claude-3-5-sonnet-20241022"})
		if model.Family != "claude-sonnet" || model.Provider != "anthropic" {
			t.Fatalf("model = %#v", model)
		}
	})
}
