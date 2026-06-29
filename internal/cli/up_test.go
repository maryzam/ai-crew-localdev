package cli

import (
	"bufio"
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/config"
	"github.com/spf13/cobra"
)

func TestFindRepoRoot(t *testing.T) {
	root := t.TempDir()
	devcontainerDir := filepath.Join(root, ".devcontainer")
	if err := os.Mkdir(devcontainerDir, 0o755); err != nil {
		t.Fatalf("mkdir .devcontainer: %v", err)
	}

	subDir := filepath.Join(root, "sub", "deep")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatalf("mkdir sub/deep: %v", err)
	}

	got, err := findRepoRoot(subDir)
	if err != nil {
		t.Fatalf("findRepoRoot: %v", err)
	}
	if got != root {
		t.Errorf("findRepoRoot(%s) = %s, want %s", subDir, got, root)
	}
}

func TestFindRepoRootGitFallback(t *testing.T) {
	root := t.TempDir()
	gitDir := filepath.Join(root, ".git")
	if err := os.Mkdir(gitDir, 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}

	got, err := findRepoRoot(root)
	if err != nil {
		t.Fatalf("findRepoRoot: %v", err)
	}
	if got != root {
		t.Errorf("findRepoRoot(%s) = %s, want %s", root, got, root)
	}
}

func TestFindRepoRootNoMarker(t *testing.T) {
	dir := t.TempDir()
	got, err := findRepoRoot(dir)
	if err != nil {
		t.Fatalf("findRepoRoot: %v", err)
	}
	// Should fall back to the original dir.
	if got != dir {
		t.Errorf("findRepoRoot(%s) = %s, want fallback to same dir", dir, got)
	}
}

func TestEnsureBrokerAlreadyRunning(t *testing.T) {
	// Start a listener to simulate a running broker.
	socketPath := filepath.Join(t.TempDir(), "broker.sock")
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	// ensureBroker should return immediately without error.
	if err := ensureBroker(socketPath); err != nil {
		t.Fatalf("ensureBroker with running broker: %v", err)
	}
}

func TestBrokerResponds(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "broker.sock")

	// No listener — should return false.
	if brokerResponds(socketPath) {
		t.Error("brokerResponds should return false for missing socket")
	}

	// Start listener — should return true.
	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	if !brokerResponds(socketPath) {
		t.Error("brokerResponds should return true for live socket")
	}
}

func TestWaitForBrokerTimeout(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "no-broker.sock")

	start := time.Now()
	result := waitForBroker(socketPath, 500*time.Millisecond)
	elapsed := time.Since(start)

	if result {
		t.Error("waitForBroker should return false when no broker is listening")
	}
	if elapsed < 400*time.Millisecond {
		t.Errorf("waitForBroker returned too quickly: %v", elapsed)
	}
}

func TestWalkUpForDevcontainerFindsDevcontainer(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".devcontainer"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	subDir := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatalf("mkdirall: %v", err)
	}

	got, found := walkUpForDevcontainer(subDir)
	if !found {
		t.Fatal("walkUpForDevcontainer should find .devcontainer/")
	}
	if got != root {
		t.Errorf("got %s, want %s", got, root)
	}
}

func TestWalkUpForDevcontainerIgnoresGit(t *testing.T) {
	// A directory with only .git/ should NOT be matched.
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	_, found := walkUpForDevcontainer(root)
	if found {
		t.Error("walkUpForDevcontainer should not match bare .git/ directory")
	}
}

func TestWalkUpForDevcontainerNotFound(t *testing.T) {
	dir := t.TempDir()
	_, found := walkUpForDevcontainer(dir)
	if found {
		t.Error("walkUpForDevcontainer should return false when no .devcontainer/ exists")
	}
}

func TestSearchLangfuseComposeFromRoot(t *testing.T) {
	root := t.TempDir()
	langfuseDir := filepath.Join(root, "contrib", "langfuse")
	if err := os.MkdirAll(langfuseDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	composePath := filepath.Join(langfuseDir, "docker-compose.yml")
	if err := os.WriteFile(composePath, []byte("services: {}"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	got, err := searchLangfuseCompose([]string{root})
	if err != nil {
		t.Fatalf("searchLangfuseCompose: %v", err)
	}
	if got != composePath {
		t.Errorf("got %q, want %q", got, composePath)
	}
}

func TestSearchLangfuseComposeWalksUp(t *testing.T) {
	root := t.TempDir()
	langfuseDir := filepath.Join(root, "contrib", "langfuse")
	if err := os.MkdirAll(langfuseDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	composePath := filepath.Join(langfuseDir, "docker-compose.yml")
	if err := os.WriteFile(composePath, []byte("services: {}"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Start from a deeply nested subdirectory — should walk up and find it.
	deepDir := filepath.Join(root, "a", "b", "c")
	if err := os.MkdirAll(deepDir, 0o755); err != nil {
		t.Fatalf("mkdirall: %v", err)
	}

	got, err := searchLangfuseCompose([]string{deepDir})
	if err != nil {
		t.Fatalf("searchLangfuseCompose: %v", err)
	}
	if got != composePath {
		t.Errorf("got %q, want %q", got, composePath)
	}
}

func TestSearchLangfuseComposeNotFound(t *testing.T) {
	emptyDir := t.TempDir()
	_, err := searchLangfuseCompose([]string{emptyDir})
	if err == nil {
		t.Error("expected error when compose file not found")
	}
}

func TestSearchLangfuseComposeTriesMultipleCandidates(t *testing.T) {
	emptyDir := t.TempDir()
	root := t.TempDir()
	langfuseDir := filepath.Join(root, "contrib", "langfuse")
	if err := os.MkdirAll(langfuseDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	composePath := filepath.Join(langfuseDir, "docker-compose.yml")
	if err := os.WriteFile(composePath, []byte("services: {}"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	// First candidate has nothing, second has the file.
	got, err := searchLangfuseCompose([]string{emptyDir, root})
	if err != nil {
		t.Fatalf("searchLangfuseCompose: %v", err)
	}
	if got != composePath {
		t.Errorf("got %q, want %q", got, composePath)
	}
}

func TestLoadLangfuseClientEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	data := []byte("LANGFUSE_INIT_PROJECT_PUBLIC_KEY=pk-test\nLANGFUSE_INIT_PROJECT_SECRET_KEY='sk-test'\n")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := loadLangfuseClientEnvironment(path); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("AI_AGENT_LANGFUSE_HOST"); got != "http://localhost:3000" {
		t.Fatalf("host = %q", got)
	}
	if got := os.Getenv("AI_AGENT_LANGFUSE_PUBLIC_KEY"); got != "pk-test" {
		t.Fatalf("public key = %q", got)
	}
	if got := os.Getenv("AI_AGENT_LANGFUSE_SECRET_KEY"); got != "sk-test" {
		t.Fatalf("secret key = %q", got)
	}
}

func TestLoadLangfuseClientEnvironmentRequiresProjectKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("NEXTAUTH_SECRET=test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := loadLangfuseClientEnvironment(path); err == nil {
		t.Fatal("missing project keys accepted")
	}
}

func TestXDGRuntimeDirPreserved(t *testing.T) {
	// Verify that RuntimeBaseDir returns existing XDG_RUNTIME_DIR value.
	original := os.Getenv("XDG_RUNTIME_DIR")
	t.Setenv("XDG_RUNTIME_DIR", "/custom/runtime")

	got := os.Getenv("XDG_RUNTIME_DIR")
	if got != "/custom/runtime" {
		t.Errorf("XDG_RUNTIME_DIR should be preserved, got %s", got)
	}

	// Restore.
	if original != "" {
		t.Setenv("XDG_RUNTIME_DIR", original)
	}
}

func TestDevcontainerExecCommand(t *testing.T) {
	repoRoot := "/tmp/ai-crew-localdev"
	got := devcontainerExecCommand(repoRoot, containerRuntimePodman)
	want := "devcontainer exec --docker-path podman --workspace-folder /tmp/ai-crew-localdev bash"
	if got != want {
		t.Fatalf("devcontainerExecCommand(%q) = %q, want %q", repoRoot, got, want)
	}
}

func TestDevcontainerExecCommandQuotesPathsWithSpaces(t *testing.T) {
	got := devcontainerExecCommand("/home/me/my project", containerRuntimePodman)
	want := "devcontainer exec --docker-path podman --workspace-folder '/home/me/my project' bash"
	if got != want {
		t.Fatalf("devcontainerExecCommand = %q, want %q", got, want)
	}
}

func TestDevcontainerExecShellCommandQuotesArgs(t *testing.T) {
	overlay := []string{"--override-config", "/run/ai agent/overlay.json"}
	got := devcontainerExecShellCommand("/home/me/my project", containerRuntimePodman, overlay)
	for _, want := range []string{
		"--workspace-folder '/home/me/my project'",
		"--override-config '/run/ai agent/overlay.json'",
		"sh -c 'if command -v bash >/dev/null 2>&1; then exec bash; else exec sh; fi'",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("devcontainerExecShellCommand = %q, missing %q", got, want)
		}
	}
}

func TestDevcontainerLabelFilter(t *testing.T) {
	repoRoot := "/tmp/ai-crew-localdev"
	got := devcontainerLabelFilter(repoRoot)
	want := "label=devcontainer.local_folder=/tmp/ai-crew-localdev"
	if got != want {
		t.Fatalf("devcontainerLabelFilter(%q) = %q, want %q", repoRoot, got, want)
	}
}

func TestWriteDevcontainerAccessInfo(t *testing.T) {
	t.Setenv("AI_AGENT_WORKSPACE", "/home/tester/github")

	var buf bytes.Buffer
	writeDevcontainerAccessInfo(&buf, "/repo/ai-crew-localdev", containerRuntimePodman)
	output := buf.String()

	for _, want := range []string{
		"devcontainer is ready; your host workspace /home/tester/github is mounted at /workspace",
		"runtime: podman",
		"re-enter later with: devcontainer exec --docker-path podman --workspace-folder /repo/ai-crew-localdev bash",
		"podman ps --filter \"label=devcontainer.local_folder=/repo/ai-crew-localdev\"",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output %q does not contain %q", output, want)
		}
	}
}

func TestWriteAgentLoginStateInfo(t *testing.T) {
	var buf bytes.Buffer
	writeAgentLoginStateInfo(&buf)
	output := buf.String()

	for _, want := range []string{
		"Claude and Codex store personal sign-in/config under /home/dev",
		"/home/dev is the ai-agent-home volume",
		"do not run 'gh auth login'",
		"ai-agent run",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output %q does not contain %q", output, want)
		}
	}
}

func TestEnsureFirstUseConfigSkipsWhenConfigExists(t *testing.T) {
	mustWriteDoctorConfig(t, t.TempDir(), true)

	origSetup := upSetupFn
	t.Cleanup(func() { upSetupFn = origSetup })
	upSetupFn = func(cmd *cobra.Command, args []string, scanner *bufio.Scanner) error {
		t.Fatal("upSetupFn should not be called when config files exist")
		return nil
	}

	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	if err := ensureFirstUseConfig(cmd, bufio.NewScanner(strings.NewReader(""))); err != nil {
		t.Fatalf("ensureFirstUseConfig: %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("expected no first-use output, got %q", buf.String())
	}
}

func TestEnsureFirstUseConfigRunsGuidedSetupWhenMissing(t *testing.T) {
	t.Setenv("AI_AGENT_CONFIG_DIR", t.TempDir())

	origSetup := upSetupFn
	t.Cleanup(func() { upSetupFn = origSetup })

	called := false
	upSetupFn = func(cmd *cobra.Command, args []string, scanner *bufio.Scanner) error {
		called = true
		return nil
	}

	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	if err := ensureFirstUseConfig(cmd, bufio.NewScanner(strings.NewReader("y\n"))); err != nil {
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

	origSetup := upSetupFn
	t.Cleanup(func() { upSetupFn = origSetup })

	upSetupFn = func(cmd *cobra.Command, args []string, scanner *bufio.Scanner) error {
		if !scanner.Scan() {
			return fmt.Errorf("expected setup input after opt-in prompt")
		}
		if got := scanner.Text(); got != "claude" {
			return fmt.Errorf("next setup input = %q, want claude", got)
		}
		return nil
	}

	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	if err := ensureFirstUseConfig(cmd, bufio.NewScanner(strings.NewReader("y\nclaude\n"))); err != nil {
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

	origSetup := upSetupFn
	t.Cleanup(func() { upSetupFn = origSetup })

	called := false
	upSetupFn = func(cmd *cobra.Command, args []string, scanner *bufio.Scanner) error {
		called = true
		return nil
	}

	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	if err := ensureFirstUseConfig(cmd, bufio.NewScanner(strings.NewReader("y\n"))); err != nil {
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

	origSetup := upSetupFn
	t.Cleanup(func() { upSetupFn = origSetup })
	upSetupFn = func(cmd *cobra.Command, args []string, scanner *bufio.Scanner) error {
		t.Fatal("upSetupFn should not be called when setup is declined")
		return nil
	}

	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	err := ensureFirstUseConfig(cmd, bufio.NewScanner(strings.NewReader("n\n")))
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
	defaultPolicyPath := config.DefaultPolicyPath()
	customPolicyPath := filepath.Join(dir, "custom-policy.json")
	t.Setenv("AI_AGENT_POLICY_PATH", customPolicyPath)
	if err := os.Rename(defaultPolicyPath, customPolicyPath); err != nil {
		t.Fatalf("move policy to custom path: %v", err)
	}

	issues, err := firstUseConfigIssues()
	if err != nil {
		t.Fatalf("firstUseConfigIssues: %v", err)
	}
	if len(issues) != 0 {
		t.Fatalf("firstUseConfigIssues = %v, want none", issues)
	}
}

func TestFirstUseConfigIssuesReportsMissingCustomPolicyPath(t *testing.T) {
	dir := t.TempDir()
	mustWriteDoctorConfig(t, dir, true)
	customPolicyPath := filepath.Join(dir, "missing-policy.json")
	t.Setenv("AI_AGENT_POLICY_PATH", customPolicyPath)

	issues, err := firstUseConfigIssues()
	if err != nil {
		t.Fatalf("firstUseConfigIssues: %v", err)
	}
	if len(issues) == 0 {
		t.Fatal("expected missing custom policy to be reported as a setup issue")
	}
	if !strings.Contains(strings.Join(issues, " "), customPolicyPath) {
		t.Fatalf("issues %v do not mention custom policy path", issues)
	}
}

func TestPromptYNAcceptsY(t *testing.T) {
	origStdin := upStdin
	t.Cleanup(func() { upStdin = origStdin })
	upStdin = strings.NewReader("y\n")

	var buf bytes.Buffer
	if !promptYN(&buf, "Install?") {
		t.Fatal("promptYN should return true for 'y' input")
	}
	if !strings.Contains(buf.String(), "Install? [y/N]") {
		t.Fatalf("expected prompt text, got: %q", buf.String())
	}
}

func TestPromptYNRejectsN(t *testing.T) {
	origStdin := upStdin
	t.Cleanup(func() { upStdin = origStdin })
	upStdin = strings.NewReader("n\n")

	var buf bytes.Buffer
	if promptYN(&buf, "Install?") {
		t.Fatal("promptYN should return false for 'n' input")
	}
}

func TestPromptYNRejectsEmpty(t *testing.T) {
	origStdin := upStdin
	t.Cleanup(func() { upStdin = origStdin })
	upStdin = strings.NewReader("\n")

	var buf bytes.Buffer
	if promptYN(&buf, "Install?") {
		t.Fatal("promptYN should return false for empty input")
	}
}

func TestPromptYNRejectsEOF(t *testing.T) {
	origStdin := upStdin
	t.Cleanup(func() { upStdin = origStdin })
	upStdin = strings.NewReader("")

	var buf bytes.Buffer
	if promptYN(&buf, "Install?") {
		t.Fatal("promptYN should return false on EOF")
	}
}

func TestTryAutoFixInvokesInstallOnRuntimeFailure(t *testing.T) {
	origInstall := upInstallFn
	t.Cleanup(func() { upInstallFn = origInstall })

	called := false
	upInstallFn = func(cmd *cobra.Command, runtime containerRuntime, scanner *bufio.Scanner) (containerRuntime, bool) {
		called = true
		return runtime, true
	}

	report := doctorReport{
		Ready: false,
		Checks: []doctorCheck{
			{Name: "container-runtime", Status: doctorStatusFail, Blocking: true},
		},
	}

	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	gotRuntime, fixed := tryAutoFix(cmd, report, containerRuntimePodman, bufio.NewScanner(strings.NewReader("")))
	if !fixed {
		t.Fatal("tryAutoFix should return true when install succeeds")
	}
	if gotRuntime != containerRuntimePodman {
		t.Fatalf("tryAutoFix changed runtime unexpectedly: got %q", gotRuntime)
	}
	if !called {
		t.Fatal("expected upInstallFn to be called")
	}
}

func TestTryAutoFixSkipsWhenNoRuntimeFailure(t *testing.T) {
	origInstall := upInstallFn
	t.Cleanup(func() { upInstallFn = origInstall })

	upInstallFn = func(cmd *cobra.Command, runtime containerRuntime, scanner *bufio.Scanner) (containerRuntime, bool) {
		t.Fatal("upInstallFn should not be called when runtime check passes")
		return runtime, false
	}

	report := doctorReport{
		Ready: false,
		Checks: []doctorCheck{
			{Name: "broker-socket", Status: doctorStatusFail, Blocking: true},
		},
	}

	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	gotRuntime, fixed := tryAutoFix(cmd, report, containerRuntimePodman, bufio.NewScanner(strings.NewReader("")))
	if fixed {
		t.Fatal("tryAutoFix should return false when no runtime failure")
	}
	if gotRuntime != containerRuntimePodman {
		t.Fatalf("tryAutoFix changed runtime unexpectedly: got %q", gotRuntime)
	}
}

func TestInstallMissingPromptsBothTools(t *testing.T) {
	origLookPath := upLookPath
	origStdin := upStdin
	origRunCmd := upRunCmd
	t.Cleanup(func() {
		upLookPath = origLookPath
		upStdin = origStdin
		upRunCmd = origRunCmd
	})

	// Both tools missing, user says yes to both.
	upLookPath = func(name string) (string, error) {
		switch name {
		case "npm":
			return "/usr/bin/npm", nil
		default:
			return "", fmt.Errorf("%s not found", name)
		}
	}
	upStdin = strings.NewReader("y\ny\n")
	upRunCmd = func(c *exec.Cmd) error { return nil } // mock success

	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	gotRuntime, fixed := installMissing(cmd, containerRuntimePodman, bufio.NewScanner(upStdin))
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
	origLookPath := upLookPath
	origStdin := upStdin
	origRunCmd := upRunCmd
	t.Cleanup(func() {
		upLookPath = origLookPath
		upStdin = origStdin
		upRunCmd = origRunCmd
	})

	// Docker is present, but the default selected runtime is Podman.
	upLookPath = func(name string) (string, error) {
		switch name {
		case "docker":
			return "/usr/bin/docker", nil
		case "npm":
			return "/usr/bin/npm", nil
		default:
			return "", fmt.Errorf("%s not found", name)
		}
	}
	upStdin = strings.NewReader("i\ny\n")
	upRunCmd = func(c *exec.Cmd) error { return nil }

	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	gotRuntime, fixed := installMissing(cmd, containerRuntimePodman, bufio.NewScanner(upStdin))
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
	origLookPath := upLookPath
	origStdin := upStdin
	origRunCmd := upRunCmd
	t.Cleanup(func() {
		upLookPath = origLookPath
		upStdin = origStdin
		upRunCmd = origRunCmd
	})

	upLookPath = func(name string) (string, error) {
		switch name {
		case "docker":
			return "/usr/bin/docker", nil
		case "npm":
			return "/usr/bin/npm", nil
		default:
			return "", fmt.Errorf("%s not found", name)
		}
	}
	upStdin = strings.NewReader("d\ny\n")
	upRunCmd = func(c *exec.Cmd) error { return nil }

	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	gotRuntime, fixed := installMissing(cmd, containerRuntimePodman, bufio.NewScanner(upStdin))
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
	origLookPath := upLookPath
	origStdin := upStdin
	origRunCmd := upRunCmd
	t.Cleanup(func() {
		upLookPath = origLookPath
		upStdin = origStdin
		upRunCmd = origRunCmd
	})

	upLookPath = func(name string) (string, error) {
		switch name {
		case "docker":
			return "/usr/bin/docker", nil
		case "npm":
			return "/usr/bin/npm", nil
		default:
			return "", fmt.Errorf("%s not found", name)
		}
	}
	upStdin = strings.NewReader("y\n")
	upRunCmd = func(c *exec.Cmd) error { return nil }

	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	gotRuntime, fixed := installMissing(cmd, containerRuntimeDocker, bufio.NewScanner(upStdin))
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
	origLookPath := upLookPath
	origStdin := upStdin
	t.Cleanup(func() {
		upLookPath = origLookPath
		upStdin = origStdin
	})

	upLookPath = func(name string) (string, error) {
		return "", fmt.Errorf("%s not found", name)
	}
	upStdin = strings.NewReader("n\nn\n")

	cmd := &cobra.Command{}
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	gotRuntime, fixed := installMissing(cmd, containerRuntimePodman, bufio.NewScanner(upStdin))
	if fixed {
		t.Fatal("installMissing should return false when user declines")
	}
	if gotRuntime != containerRuntimePodman {
		t.Fatalf("installMissing changed runtime unexpectedly: got %q", gotRuntime)
	}
}
