package capabilities

import "testing"

func TestRegistryDeclaresClaudeAndCodexCapabilities(t *testing.T) {
	for _, name := range []string{"claude", "codex"} {
		entry, ok := Find(name)
		if !ok {
			t.Fatalf("Find(%q) failed", name)
		}
		if len(entry.Tools) == 0 || !entry.NativeTelemetry.Supported || entry.NativeTelemetry.Surface == "" {
			t.Fatalf("%s execution declarations are incomplete: %#v", name, entry)
		}
		if len(entry.Login.Probe) == 0 || entry.Login.InstallHint == "" || entry.Login.Remediation == "" {
			t.Fatalf("%s login declaration is incomplete: %#v", name, entry.Login)
		}
		if len(entry.ProjectedPaths) == 0 || len(entry.GuidanceTargets) == 0 || DefaultToolForAgent(name) == "" {
			t.Fatalf("%s home/default declarations are incomplete: %#v", name, entry)
		}
	}
}

func TestCommandMatchingUsesAgentToolAliases(t *testing.T) {
	if !CommandMatchesTool("/usr/local/bin/claude", "claude-code") {
		t.Fatal("claude executable should satisfy claude-code tool")
	}
	if CommandMatchesTool("codex", "claude-code") {
		t.Fatal("codex executable must not satisfy claude-code tool")
	}
	if !CommandMatchesTool("custom-tool", "custom-tool") {
		t.Fatal("unknown tools should still match exactly")
	}
	if got := DefaultToolForAgent("codex-reviewer"); got != "" {
		t.Fatalf("DefaultToolForAgent fuzzy matched %q", got)
	}
}

func TestTelemetryDeclarations(t *testing.T) {
	if telemetry, ok := NativeTelemetryForCommand([]string{"claude"}); !ok || telemetry.Surface != NativeTelemetryEnv {
		t.Fatalf("claude telemetry = %#v, %t", telemetry, ok)
	}
	if telemetry, ok := NativeTelemetryForCommand([]string{"codex"}); !ok || telemetry.Surface != NativeTelemetryCommand {
		t.Fatalf("codex telemetry = %#v, %t", telemetry, ok)
	}
}

func TestResolveAttributionUsesAgentDeclarations(t *testing.T) {
	for _, key := range []string{"AI_AGENT_MODEL", "CODEX_MODEL", "OPENAI_MODEL", "CLAUDE_MODEL", "ANTHROPIC_MODEL", "GEMINI_MODEL"} {
		t.Setenv(key, "")
	}

	t.Run("claude reviewer infers declared type", func(t *testing.T) {
		agent, model := ResolveAttribution("claude-reviewer", "", []string{"claude"})
		if agent.Type != "claude_code" || agent.Command != "claude" {
			t.Fatalf("agent attribution = %#v", agent)
		}
		if model.Provider != "anthropic" || model.Family != "claude" || model.Resolution.Status != "partial" {
			t.Fatalf("model attribution = %#v", model)
		}
	})

	t.Run("cli model wins and conflict is retained", func(t *testing.T) {
		t.Setenv("OPENAI_MODEL", "gpt-4.1")
		_, model := ResolveAttribution("codex", "o3", []string{"codex", "--model", "gpt-5.2-codex"})
		if model.Requested != "gpt-5.2-codex" || model.Family != "gpt-5" {
			t.Fatalf("model attribution = %#v", model)
		}
		if model.Resolution.PrimarySource != "cli" || !model.Resolution.Conflict || len(model.Resolution.Sources) != 3 {
			t.Fatalf("resolution = %#v", model.Resolution)
		}
	})

	t.Run("unknown agent is unresolved", func(t *testing.T) {
		agent, model := ResolveAttribution("custom", "", []string{"custom-agent"})
		if agent.Type != "other" || model.Resolution.Status != "unresolved" || model.Provider != "" || model.Family != "" {
			t.Fatalf("agent = %#v model = %#v", agent, model)
		}
	})
}

func TestRegistryProjectionsAreImmutable(t *testing.T) {
	first := ProjectedHomePaths()
	first[2].Exclude[0] = "changed"
	second := ProjectedHomePaths()
	if second[2].Exclude[0] != "packages" {
		t.Fatalf("ProjectedHomePaths shared exclude slice: %#v", second)
	}

	entry, _ := Find("codex")
	entry.Login.Probe[0] = "changed"
	next, _ := Find("codex")
	if next.Login.Probe[0] != "login" {
		t.Fatalf("Find shared login probe slice: %#v", next.Login.Probe)
	}
}
