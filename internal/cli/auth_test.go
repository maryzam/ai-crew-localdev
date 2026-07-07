package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/app/agentauth"
)

func writeFakeAgent(t *testing.T, dir, name, script string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestAuthStatusCommandReportsPersistedLogins(t *testing.T) {
	binDir := t.TempDir()
	writeFakeAgent(t, binDir, "claude", `printf '%s' '{"loggedIn":true,"authMethod":"claude.ai","apiProvider":"firstParty"}'`+"\n")
	writeFakeAgent(t, binDir, "codex", `echo 'Logged in using an API key'`+"\n")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	command := newAuthCommand()
	command.SetArgs([]string{"status", "--json"})
	var output bytes.Buffer
	command.SetOut(&output)
	if err := command.Execute(); err != nil {
		t.Fatalf("auth status: %v\n%s", err, output.String())
	}
	var report agentauth.Report
	if err := json.Unmarshal(output.Bytes(), &report); err != nil {
		t.Fatalf("decode: %v\n%s", err, output.String())
	}
	if !report.AllLoggedIn() {
		t.Fatalf("expected all agents logged in: %#v", report.Agents)
	}
}

func TestAuthStatusCommandRemediatesMissingLogin(t *testing.T) {
	binDir := t.TempDir()
	writeFakeAgent(t, binDir, "claude", `printf '%s' '{"loggedIn":false,"authMethod":"none"}'`+"\n")
	writeFakeAgent(t, binDir, "codex", `echo 'Not logged in'; exit 1`+"\n")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	command := newAuthCommand()
	command.SetArgs([]string{"status"})
	var output bytes.Buffer
	command.SetOut(&output)
	if err := command.Execute(); err != nil {
		t.Fatalf("auth status: %v\n%s", err, output.String())
	}
	text := output.String()
	if !strings.Contains(text, "[logged_out] claude") || !strings.Contains(text, "[logged_out] codex") {
		t.Fatalf("expected logged_out lines, got:\n%s", text)
	}
	if !strings.Contains(text, "persists in /home/dev") {
		t.Fatalf("expected remediation guidance, got:\n%s", text)
	}
}

func TestWriteAuthTextContract(t *testing.T) {
	report := agentauth.Report{Agents: []agentauth.AgentReport{
		{Agent: "claude", Status: agentauth.StatusLoggedIn, Method: "claude.ai"},
		{Agent: "codex", Status: agentauth.StatusLoggedOut, Remediation: "run 'codex login'"},
	}}
	var output bytes.Buffer
	writeAuthText(&output, report)
	want := "ai-agent auth status\n[logged_in] claude: claude.ai\n[logged_out] codex\n  fix: run 'codex login'\nsome agents need login; personal login state persists in /home/dev, GitHub access stays brokered\n"
	if output.String() != want {
		t.Fatalf("output = %q, want %q", output.String(), want)
	}
}

func TestRunAuthProbeCapturesNonZeroExit(t *testing.T) {
	binDir := t.TempDir()
	writeFakeAgent(t, binDir, "codex", `echo 'Not logged in'; exit 1`+"\n")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	result := runAuthProbe(t.Context(), "codex", "login", "status")
	if result.Err != nil {
		t.Fatalf("expected non-zero exit to be captured, not errored: %v", result.Err)
	}
	if result.ExitCode != 1 {
		t.Fatalf("exit code = %d, want 1", result.ExitCode)
	}
	if !strings.Contains(string(result.Stdout), "Not logged in") {
		t.Fatalf("stdout = %q", result.Stdout)
	}
}

func TestRunAuthProbeMarksTimeout(t *testing.T) {
	binDir := t.TempDir()
	writeFakeAgent(t, binDir, "codex", "sleep 5\n")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	result := runAuthProbe(ctx, "codex", "login", "status")
	if !result.TimedOut {
		t.Fatalf("expected TimedOut on deadline, got %#v", result)
	}
}

func TestRunAuthProbeBoundsOutput(t *testing.T) {
	binDir := t.TempDir()
	writeFakeAgent(t, binDir, "codex", "yes ai-agent | head -c 200000\n")
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	result := runAuthProbe(t.Context(), "codex", "login", "status")
	if len(result.Stdout) > agentauth.MaxProbeOutput {
		t.Fatalf("stdout not bounded: %d bytes, cap %d", len(result.Stdout), agentauth.MaxProbeOutput)
	}
	if !result.Truncated {
		t.Fatal("over-budget output must be flagged as truncated")
	}
}
