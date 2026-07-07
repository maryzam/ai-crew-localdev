package agentauth

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeProbe struct {
	stdout    string
	stderr    string
	exitCode  int
	timedOut  bool
	truncated bool
	err       error
}

func newService(installed map[string]bool, probes map[string]fakeProbe) Service {
	return New(Dependencies{
		FindBinary: func(name string) (string, error) {
			if installed[name] {
				return "/usr/local/bin/" + name, nil
			}
			return "", errors.New("exec: \"" + name + "\": executable file not found in $PATH")
		},
		Run: func(_ context.Context, name string, _ ...string) ProbeResult {
			probe := probes[name]
			return ProbeResult{Stdout: []byte(probe.stdout), Stderr: []byte(probe.stderr), ExitCode: probe.exitCode, TimedOut: probe.timedOut, Truncated: probe.truncated, Err: probe.err}
		},
	})
}

func agentByName(t *testing.T, report Report, name string) AgentReport {
	t.Helper()
	for _, agent := range report.Agents {
		if agent.Agent == name {
			return agent
		}
	}
	t.Fatalf("no report for agent %q in %#v", name, report.Agents)
	return AgentReport{}
}

func TestClaudeOAuthReuseReportsLoggedIn(t *testing.T) {
	service := newService(
		map[string]bool{"claude": true, "codex": true},
		map[string]fakeProbe{
			"claude": {stdout: `{"loggedIn":true,"authMethod":"claude.ai","apiProvider":"firstParty","subscriptionType":"max"}`},
			"codex":  {stdout: "Logged in using an API key\n"},
		},
	)
	report := service.Status(context.Background())
	claude := agentByName(t, report, "claude")
	if claude.Status != StatusLoggedIn {
		t.Fatalf("claude status = %q, want logged_in", claude.Status)
	}
	if claude.Method != "claude.ai" {
		t.Fatalf("claude method = %q, want claude.ai", claude.Method)
	}
	if claude.Remediation != "" {
		t.Fatalf("logged-in claude should not carry remediation, got %q", claude.Remediation)
	}
	if !report.AllLoggedIn() {
		t.Fatalf("expected AllLoggedIn for %#v", report.Agents)
	}
}

func TestClaudeAPIKeyHelperReportsSource(t *testing.T) {
	service := newService(
		map[string]bool{"claude": true, "codex": true},
		map[string]fakeProbe{
			"claude": {stdout: `{"loggedIn":true,"authMethod":"api_key_helper","apiProvider":"firstParty","apiKeySource":"apiKeyHelper"}`},
			"codex":  {stdout: "Logged in using an API key\n"},
		},
	)
	claude := agentByName(t, service.Status(context.Background()), "claude")
	if claude.Status != StatusLoggedIn || claude.Method != "api_key_helper" || claude.Source != "apiKeyHelper" {
		t.Fatalf("api key helper report = %#v", claude)
	}
}

func TestClaudeLoggedOutCarriesRemediation(t *testing.T) {
	service := newService(
		map[string]bool{"claude": true, "codex": true},
		map[string]fakeProbe{
			"claude": {stdout: `{"loggedIn":false,"authMethod":"none","apiProvider":"firstParty"}`, exitCode: 1},
			"codex":  {stdout: "Not logged in\n", exitCode: 1},
		},
	)
	report := service.Status(context.Background())
	claude := agentByName(t, report, "claude")
	if claude.Status != StatusLoggedOut {
		t.Fatalf("claude status = %q, want logged_out", claude.Status)
	}
	if claude.Remediation == "" {
		t.Fatal("logged-out claude must carry remediation")
	}
	codex := agentByName(t, report, "codex")
	if codex.Status != StatusLoggedOut {
		t.Fatalf("codex status = %q, want logged_out", codex.Status)
	}
	if report.AllLoggedIn() {
		t.Fatal("AllLoggedIn must be false when an agent is logged out")
	}
}

func TestClaudeMissingBinaryReportsNotInstalled(t *testing.T) {
	service := newService(
		map[string]bool{"codex": true},
		map[string]fakeProbe{"codex": {stdout: "Logged in using an API key\n"}},
	)
	claude := agentByName(t, service.Status(context.Background()), "claude")
	if claude.Status != StatusNotInstalled {
		t.Fatalf("claude status = %q, want not_installed", claude.Status)
	}
	if claude.Remediation == "" {
		t.Fatal("not-installed claude must carry an install hint")
	}
}

func TestClaudeUnparsableOutputReportsUnknown(t *testing.T) {
	service := newService(
		map[string]bool{"claude": true, "codex": true},
		map[string]fakeProbe{
			"claude": {stderr: "boom", exitCode: 2, err: errors.New("exit status 2")},
			"codex":  {stdout: "Logged in using an API key\n"},
		},
	)
	claude := agentByName(t, service.Status(context.Background()), "claude")
	if claude.Status != StatusUnknown {
		t.Fatalf("claude status = %q, want unknown", claude.Status)
	}
	if claude.Detail == "" {
		t.Fatal("unknown status must explain why")
	}
}

func TestLoggedInClaimWithNonZeroExitReportsUnknown(t *testing.T) {
	service := newService(
		map[string]bool{"claude": true, "codex": true},
		map[string]fakeProbe{
			"claude": {stdout: `{"loggedIn":true,"authMethod":"claude.ai"}`, exitCode: 1},
			"codex":  {stdout: "Logged in using an API key\n", exitCode: 3},
		},
	)
	report := service.Status(context.Background())
	claude := agentByName(t, report, "claude")
	if claude.Status != StatusUnknown || claude.Method != "" {
		t.Fatalf("claude with contradictory exit = %#v, want unknown without method", claude)
	}
	codex := agentByName(t, report, "codex")
	if codex.Status != StatusUnknown || codex.Method != "" {
		t.Fatalf("codex with contradictory exit = %#v, want unknown without method", codex)
	}
}

func TestProbeTimeoutReportsUnknown(t *testing.T) {
	service := newService(
		map[string]bool{"claude": true, "codex": true},
		map[string]fakeProbe{
			"claude": {timedOut: true},
			"codex":  {timedOut: true},
		},
	)
	report := service.Status(context.Background())
	for _, agent := range report.Agents {
		if agent.Status != StatusUnknown {
			t.Fatalf("%s on timeout = %q, want unknown", agent.Agent, agent.Status)
		}
		if !strings.Contains(agent.Detail, "exceeded") {
			t.Fatalf("%s timeout detail = %q, want mention of the budget", agent.Agent, agent.Detail)
		}
	}
}

func TestTruncatedOutputReportsUnknown(t *testing.T) {
	service := newService(
		map[string]bool{"claude": true, "codex": true},
		map[string]fakeProbe{
			"claude": {stdout: `{"loggedIn":true,"authMethod":"claude.ai"}`, truncated: true},
			"codex":  {stdout: "Logged in using an API key\n", truncated: true},
		},
	)
	report := service.Status(context.Background())
	for _, agent := range report.Agents {
		if agent.Status != StatusUnknown {
			t.Fatalf("%s with truncated output = %q, want unknown", agent.Agent, agent.Status)
		}
		if !strings.Contains(agent.Detail, "exceeded") {
			t.Fatalf("%s truncation detail = %q, want mention of the limit", agent.Agent, agent.Detail)
		}
	}
}

func TestCodexChatGPTLoginReportsMethod(t *testing.T) {
	service := newService(
		map[string]bool{"claude": true, "codex": true},
		map[string]fakeProbe{
			"claude": {stdout: `{"loggedIn":true,"authMethod":"claude.ai"}`},
			"codex":  {stdout: "Logged in using ChatGPT\n"},
		},
	)
	codex := agentByName(t, service.Status(context.Background()), "codex")
	if codex.Status != StatusLoggedIn || codex.Method != "Logged in using ChatGPT" {
		t.Fatalf("codex report = %#v", codex)
	}
}
