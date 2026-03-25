package cli

import (
	"bytes"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	got := devcontainerExecCommand(repoRoot)
	want := "devcontainer exec --workspace-folder /tmp/ai-crew-localdev bash"
	if got != want {
		t.Fatalf("devcontainerExecCommand(%q) = %q, want %q", repoRoot, got, want)
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
	writeDevcontainerAccessInfo(&buf, "/repo/ai-crew-localdev")
	output := buf.String()

	for _, want := range []string{
		"devcontainer is ready; your host workspace /home/tester/github is mounted at /workspace",
		"re-enter later with: devcontainer exec --workspace-folder /repo/ai-crew-localdev bash",
		"docker ps --filter \"label=devcontainer.local_folder=/repo/ai-crew-localdev\"",
		"podman ps --filter \"label=devcontainer.local_folder=/repo/ai-crew-localdev\"",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("output %q does not contain %q", output, want)
		}
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
	upInstallFn = func(cmd *cobra.Command) bool {
		called = true
		return true
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

	if !tryAutoFix(cmd, report) {
		t.Fatal("tryAutoFix should return true when install succeeds")
	}
	if !called {
		t.Fatal("expected upInstallFn to be called")
	}
}

func TestTryAutoFixSkipsWhenNoRuntimeFailure(t *testing.T) {
	origInstall := upInstallFn
	t.Cleanup(func() { upInstallFn = origInstall })

	upInstallFn = func(cmd *cobra.Command) bool {
		t.Fatal("upInstallFn should not be called when runtime check passes")
		return false
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

	if tryAutoFix(cmd, report) {
		t.Fatal("tryAutoFix should return false when no runtime failure")
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

	if !installMissing(cmd) {
		t.Fatal("installMissing should return true when both installs succeed")
	}
	output := buf.String()
	if !strings.Contains(output, "Podman is not installed") {
		t.Error("expected podman install prompt")
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

	if installMissing(cmd) {
		t.Fatal("installMissing should return false when user declines")
	}
}
