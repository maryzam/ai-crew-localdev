package capabilities

import (
	"reflect"
	"testing"
)

func TestRegistryDeclaresClaudeAndCodexCapabilities(t *testing.T) {
	for _, name := range []string{"claude", "codex"} {
		entry, ok := Find(name)
		if !ok {
			t.Fatalf("Find(%q) failed", name)
		}
		if entry.Type == "" || entry.Provider == "" || entry.ModelFamily == "" {
			t.Fatalf("%s model attribution is incomplete: %#v", name, entry)
		}
		if len(entry.Tools) == 0 || len(entry.ModelEnv) == 0 || !entry.NativeTelemetry.Supported || entry.NativeTelemetry.Surface == "" {
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

func TestModelAndTelemetryDeclarations(t *testing.T) {
	if got := InferType("claude-reviewer", []string{"ignored"}); got != "claude_code" {
		t.Fatalf("InferType claude = %q", got)
	}
	if got := InferType("unknown", []string{"/opt/bin/codex.exe"}); got != "codex" {
		t.Fatalf("InferType codex = %q", got)
	}
	if got := ProviderForType("claude_code"); got != "anthropic" {
		t.Fatalf("ProviderForType claude_code = %q", got)
	}
	if got := FamilyForType("codex"); got != "openai-codex" {
		t.Fatalf("FamilyForType codex = %q", got)
	}
	if got := ModelEnvKeys("codex"); !reflect.DeepEqual(got, []string{"AI_AGENT_MODEL", "CODEX_MODEL", "OPENAI_MODEL"}) {
		t.Fatalf("ModelEnvKeys codex = %#v", got)
	}
	if telemetry, ok := NativeTelemetryForCommand([]string{"claude"}); !ok || telemetry.Surface != NativeTelemetryEnv {
		t.Fatalf("claude telemetry = %#v, %t", telemetry, ok)
	}
	if telemetry, ok := NativeTelemetryForCommand([]string{"codex"}); !ok || telemetry.Surface != NativeTelemetryCommand {
		t.Fatalf("codex telemetry = %#v, %t", telemetry, ok)
	}
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
