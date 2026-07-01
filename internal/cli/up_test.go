package cli

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maryzam/ai-crew-localdev/internal/platform/paths"
	"github.com/maryzam/ai-crew-localdev/internal/readiness"
	"github.com/spf13/cobra"
)

func TestNewUpCommandOwnsFlagState(t *testing.T) {
	first := newUpCommand(setupTestServices)
	if err := first.Flags().Set("workspace", "/first"); err != nil {
		t.Fatal(err)
	}
	second := newUpCommand(setupTestServices)
	workspace, err := second.Flags().GetString("workspace")
	if err != nil {
		t.Fatal(err)
	}
	if workspace != "." {
		t.Fatalf("workspace = %q, want default", workspace)
	}
}

func TestEnsureFirstUseConfigSkipsWhenConfigExists(t *testing.T) {
	mustWriteDoctorConfig(t, t.TempDir(), true)
	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	adapter := testUpAdapter(cmd, "")
	adapter.guidedSetup = func(*bufio.Scanner) error {
		t.Fatal("guided setup should not run when config files exist")
		return nil
	}
	if err := adapter.EnsureConfigured(); err != nil {
		t.Fatalf("ensureFirstUseConfig: %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("expected no first-use output, got %q", buf.String())
	}
}

func TestEnsureFirstUseConfigRunsGuidedSetupWhenMissing(t *testing.T) {
	t.Setenv("AI_AGENT_CONFIG_DIR", t.TempDir())
	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	adapter := testUpAdapter(cmd, "y\n")
	called := false
	adapter.guidedSetup = func(*bufio.Scanner) error {
		called = true
		return nil
	}
	if err := adapter.EnsureConfigured(); err != nil {
		t.Fatalf("ensureFirstUseConfig: %v", err)
	}
	if !called {
		t.Fatal("expected guided setup to run")
	}
	output := buf.String()
	for _, want := range []string{
		"first-time configuration needs attention",
		"identities.json",
		"policy.json",
		"Run guided setup now? [y/N]",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output %q does not contain %q", output, want)
		}
	}
}

func TestEnsureFirstUseConfigUsesOneScannerForPromptAndSetup(t *testing.T) {
	t.Setenv("AI_AGENT_CONFIG_DIR", t.TempDir())
	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	adapter := testUpAdapter(cmd, "y\nclaude\n")
	adapter.guidedSetup = func(scanner *bufio.Scanner) error {
		if !scanner.Scan() {
			return fmt.Errorf("expected setup input after opt-in prompt")
		}
		if got := scanner.Text(); got != "claude" {
			return fmt.Errorf("next setup input = %q, want claude", got)
		}
		return nil
	}
	if err := adapter.EnsureConfigured(); err != nil {
		t.Fatalf("ensureFirstUseConfig: %v", err)
	}
}

func TestEnsureFirstUseConfigTreatsInvalidFilesAsSetupIssues(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("AI_AGENT_CONFIG_DIR", configDir)
	if err := os.WriteFile(filepath.Join(configDir, "identities.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "policy.json"), []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	adapter := testUpAdapter(cmd, "y\n")
	called := false
	adapter.guidedSetup = func(*bufio.Scanner) error {
		called = true
		return nil
	}
	if err := adapter.EnsureConfigured(); err != nil {
		t.Fatalf("ensureFirstUseConfig: %v", err)
	}
	if !called {
		t.Fatal("expected invalid config to route through guided setup")
	}
	output := buf.String()
	if !strings.Contains(output, "unsupported schema version") || !strings.Contains(output, "schema_version") {
		t.Fatalf("output %q does not describe invalid config", output)
	}
}

func TestEnsureFirstUseConfigFailsClosedWhenSetupDeclined(t *testing.T) {
	t.Setenv("AI_AGENT_CONFIG_DIR", t.TempDir())
	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	adapter := testUpAdapter(cmd, "n\n")
	adapter.guidedSetup = func(*bufio.Scanner) error {
		t.Fatal("guided setup should not run when setup is declined")
		return nil
	}
	err := adapter.EnsureConfigured()
	if err == nil {
		t.Fatal("expected first-use setup error")
	}
	if !strings.Contains(err.Error(), "first-time configuration is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestFirstUseConfigIssuesHonorsCustomPolicyPath(t *testing.T) {
	dir := t.TempDir()
	mustWriteDoctorConfig(t, dir, true)
	defaultPolicyPath := paths.DefaultPolicyPath()
	customPolicyPath := filepath.Join(dir, "custom-policy.json")
	t.Setenv("AI_AGENT_POLICY_PATH", customPolicyPath)
	if err := os.Rename(defaultPolicyPath, customPolicyPath); err != nil {
		t.Fatalf("move policy to custom path: %v", err)
	}

	issues := firstUseConfigIssues(newReadinessService(testPolicyValidator))
	if len(issues) != 0 {
		t.Fatalf("firstUseConfigIssues = %v, want none", issues)
	}
}

func TestFirstUseConfigIssuesReportsMissingCustomPolicyPath(t *testing.T) {
	dir := t.TempDir()
	mustWriteDoctorConfig(t, dir, true)
	customPolicyPath := filepath.Join(dir, "missing-policy.json")
	t.Setenv("AI_AGENT_POLICY_PATH", customPolicyPath)

	issues := firstUseConfigIssues(newReadinessService(testPolicyValidator))
	if len(issues) == 0 {
		t.Fatal("expected missing custom policy to be reported as a setup issue")
	}
	if !strings.Contains(strings.Join(issues, " "), customPolicyPath) {
		t.Fatalf("issues %v do not mention custom policy path", issues)
	}
}

func TestPromptYNDefaultsToNo(t *testing.T) {
	for _, test := range []struct {
		name  string
		input string
		want  bool
	}{
		{name: "yes", input: "y\n", want: true},
		{name: "no", input: "n\n"},
		{name: "empty", input: "\n"},
		{name: "EOF"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			adapter := testUpAdapter(&cobra.Command{}, test.input)
			if got := promptYNWithScanner(&output, adapter.scanner, "Install?"); got != test.want {
				t.Fatalf("answer = %t, want %t", got, test.want)
			}
			if output.String() != "Install? [y/N] " {
				t.Fatalf("prompt = %q", output.String())
			}
		})
	}
}

func TestTryAutoFixInvokesInstallOnRuntimeFailure(t *testing.T) {
	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	adapter := testUpAdapter(cmd, "")
	called := false
	adapter.install = func(runtime containerRuntime, scanner *bufio.Scanner) (containerRuntime, bool) {
		called = true
		return runtime, true
	}

	report := readiness.Report{
		Ready: false,
		Checks: []readiness.Check{
			{Name: "container-runtime", Status: readiness.StatusFail},
		},
	}

	gotRuntime, fixed := adapter.tryAutoFix(report, containerRuntimePodman, adapter.scanner)
	if !fixed {
		t.Fatal("tryAutoFix should return true when install succeeds")
	}
	if gotRuntime != containerRuntimePodman {
		t.Fatalf("tryAutoFix changed runtime unexpectedly: got %q", gotRuntime)
	}
	if !called {
		t.Fatal("expected install to be called")
	}
}

func TestTryAutoFixSkipsWhenNoRuntimeFailure(t *testing.T) {
	report := readiness.Report{
		Ready: false,
		Checks: []readiness.Check{
			{Name: "broker-socket", Status: readiness.StatusFail},
		},
	}

	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	adapter := testUpAdapter(cmd, "")
	adapter.install = func(runtime containerRuntime, scanner *bufio.Scanner) (containerRuntime, bool) {
		t.Fatal("install should not be called when runtime check passes")
		return runtime, false
	}
	gotRuntime, fixed := adapter.tryAutoFix(report, containerRuntimePodman, adapter.scanner)
	if fixed {
		t.Fatal("tryAutoFix should return false when no runtime failure")
	}
	if gotRuntime != containerRuntimePodman {
		t.Fatalf("tryAutoFix changed runtime unexpectedly: got %q", gotRuntime)
	}
}

func TestInstallMissingPromptsBothTools(t *testing.T) {
	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	adapter := testUpAdapter(cmd, "y\ny\n")
	adapter.lookPath = func(name string) (string, error) {
		switch name {
		case "npm":
			return "/usr/bin/npm", nil
		default:
			return "", fmt.Errorf("%s not found", name)
		}
	}
	adapter.runCommand = func(c *exec.Cmd) error { return nil }
	gotRuntime, fixed := adapter.installMissing(containerRuntimePodman, adapter.scanner)
	if !fixed {
		t.Fatal("installMissing should return true when both installs succeed")
	}
	if gotRuntime != containerRuntimePodman {
		t.Fatalf("installMissing changed runtime unexpectedly: got %q", gotRuntime)
	}
	output := buf.String()
	if !strings.Contains(output, "Selected runtime podman is not installed") {
		t.Error("expected runtime install prompt")
	}
	if !strings.Contains(output, "devcontainer CLI is not installed") {
		t.Error("expected devcontainer install prompt")
	}
}

func TestInstallMissingOffersPodmanInstallOrDockerFallback(t *testing.T) {
	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	adapter := testUpAdapter(cmd, "i\ny\n")
	adapter.lookPath = func(name string) (string, error) {
		switch name {
		case "docker":
			return "/usr/bin/docker", nil
		case "npm":
			return "/usr/bin/npm", nil
		default:
			return "", fmt.Errorf("%s not found", name)
		}
	}
	adapter.runCommand = func(c *exec.Cmd) error { return nil }
	gotRuntime, fixed := adapter.installMissing(containerRuntimePodman, adapter.scanner)
	if !fixed {
		t.Fatal("installMissing should return true when podman and devcontainer installs succeed")
	}
	if gotRuntime != containerRuntimePodman {
		t.Fatalf("installMissing should keep podman after install choice, got %q", gotRuntime)
	}
	output := buf.String()
	if !strings.Contains(output, "Choose: [i] install Podman and continue, [d] use Docker for this run") {
		t.Error("expected podman fallback choice prompt")
	}
	if !strings.Contains(output, "devcontainer CLI is not installed") {
		t.Error("expected devcontainer install prompt")
	}
}

func TestInstallMissingCanUseDockerForCurrentRun(t *testing.T) {
	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	adapter := testUpAdapter(cmd, "d\ny\n")
	adapter.lookPath = func(name string) (string, error) {
		switch name {
		case "docker":
			return "/usr/bin/docker", nil
		case "npm":
			return "/usr/bin/npm", nil
		default:
			return "", fmt.Errorf("%s not found", name)
		}
	}
	adapter.runCommand = func(c *exec.Cmd) error { return nil }
	gotRuntime, fixed := adapter.installMissing(containerRuntimePodman, adapter.scanner)
	if !fixed {
		t.Fatal("installMissing should return true when docker fallback and devcontainer install succeed")
	}
	if gotRuntime != containerRuntimeDocker {
		t.Fatalf("installMissing should switch runtime to docker, got %q", gotRuntime)
	}
	output := buf.String()
	if !strings.Contains(output, "using docker for this run") {
		t.Error("expected docker fallback message")
	}
}

func TestInstallMissingSkipsPodmanPromptWhenDockerSelected(t *testing.T) {
	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	adapter := testUpAdapter(cmd, "y\n")
	adapter.lookPath = func(name string) (string, error) {
		switch name {
		case "docker":
			return "/usr/bin/docker", nil
		case "npm":
			return "/usr/bin/npm", nil
		default:
			return "", fmt.Errorf("%s not found", name)
		}
	}
	adapter.runCommand = func(c *exec.Cmd) error { return nil }
	gotRuntime, fixed := adapter.installMissing(containerRuntimeDocker, adapter.scanner)
	if !fixed {
		t.Fatal("installMissing should return true when devcontainer install succeeds")
	}
	if gotRuntime != containerRuntimeDocker {
		t.Fatalf("installMissing changed runtime unexpectedly: got %q", gotRuntime)
	}
	output := buf.String()
	if strings.Contains(output, "Selected runtime podman is not installed") {
		t.Error("should not prompt for podman when docker is explicitly selected")
	}
	if !strings.Contains(output, "devcontainer CLI is not installed") {
		t.Error("expected devcontainer install prompt")
	}
}

func TestInstallMissingUserDeclinesAll(t *testing.T) {
	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	adapter := testUpAdapter(cmd, "n\nn\n")
	adapter.lookPath = func(name string) (string, error) { return "", fmt.Errorf("%s not found", name) }
	gotRuntime, fixed := adapter.installMissing(containerRuntimePodman, adapter.scanner)
	if fixed {
		t.Fatal("installMissing should return false when user declines")
	}
	if gotRuntime != containerRuntimePodman {
		t.Fatalf("installMissing changed runtime unexpectedly: got %q", gotRuntime)
	}
}

func testUpAdapter(command *cobra.Command, input string) *upCLIAdapter {
	adapter := newUpCLIAdapter(command, setupTestServices)
	adapter.stdin = strings.NewReader(input)
	adapter.scanner = bufio.NewScanner(adapter.stdin)
	return adapter
}
