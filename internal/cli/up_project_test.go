package cli

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestProjectHasDevcontainer(t *testing.T) {
	cases := []struct {
		name  string
		setup func(dir string)
		want  bool
	}{
		{"nested", func(dir string) { mustWriteFile(t, filepath.Join(dir, ".devcontainer", "devcontainer.json"), "{}") }, true},
		{"root", func(dir string) { mustWriteFile(t, filepath.Join(dir, ".devcontainer.json"), "{}") }, true},
		{"none", func(string) {}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			c.setup(dir)
			if got := projectHasDevcontainer(dir); got != c.want {
				t.Fatalf("projectHasDevcontainer = %v, want %v", got, c.want)
			}
		})
	}
}

func TestProjectUpArgsCarriesWorkspaceOverlayAndBuild(t *testing.T) {
	overlay := []string{"--mount", "type=bind,source=/x,target=/run/ai-agent"}
	args := projectUpArgs(containerRuntimePodman, "/home/me/app", overlay, true)

	joined := strings.Join(args, " ")
	for _, want := range []string{
		"up --docker-path podman",
		"--workspace-folder /home/me/app",
		"--mount type=bind,source=/x,target=/run/ai-agent",
		"--build-no-cache",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("projectUpArgs missing %q in %q", want, joined)
		}
	}
}

func TestProjectUpArgsOmitsBuildByDefault(t *testing.T) {
	args := projectUpArgs(containerRuntimePodman, "/app", nil, false)
	if strings.Contains(strings.Join(args, " "), "--build-no-cache") {
		t.Fatal("projectUpArgs should not force a rebuild when build is false")
	}
}

func TestBrokerOverlayArgsInjectsSocketAndToolchain(t *testing.T) {
	binDir := t.TempDir()
	for _, b := range aiAgentBinaries {
		mustWriteFile(t, filepath.Join(binDir, b), "")
	}
	fakeSelf := filepath.Join(binDir, "ai-agent")

	orig := osExecutable
	osExecutable = func() (string, error) { return fakeSelf, nil }
	t.Cleanup(func() { osExecutable = orig })

	args, err := brokerOverlayArgs()
	if err != nil {
		t.Fatalf("brokerOverlayArgs: %v", err)
	}

	joined := strings.Join(args, " ")
	for _, want := range []string{
		"--mount type=bind,source=" + binDir + "/ai-agent-gh,target=/usr/local/ai-agent/bin/ai-agent-gh,readonly",
		"target=/run/ai-agent,readonly",
		"AI_AGENT_AUTH_SOCK=/run/ai-agent/broker.sock",
		"PATH=/usr/local/ai-agent/bin:${containerEnv:PATH}",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("brokerOverlayArgs missing %q in %q", want, joined)
		}
	}
}

func TestBrokerOverlayArgsMountsAreReadOnly(t *testing.T) {
	binDir := t.TempDir()
	for _, b := range aiAgentBinaries {
		mustWriteFile(t, filepath.Join(binDir, b), "")
	}
	orig := osExecutable
	osExecutable = func() (string, error) { return filepath.Join(binDir, "ai-agent"), nil }
	t.Cleanup(func() { osExecutable = orig })

	args, err := brokerOverlayArgs()
	if err != nil {
		t.Fatalf("brokerOverlayArgs: %v", err)
	}
	for i, a := range args {
		if i == 0 || args[i-1] != "--mount" {
			continue
		}
		if !strings.Contains(a, ",readonly") {
			t.Fatalf("overlay mount not read-only: %q", a)
		}
	}
}

func TestLaunchProjectDevcontainerOrchestratesUpThenShell(t *testing.T) {
	project := t.TempDir()
	mustWriteFile(t, filepath.Join(project, ".devcontainer", "devcontainer.json"), "{}")

	binDir := t.TempDir()
	for _, b := range aiAgentBinaries {
		mustWriteFile(t, filepath.Join(binDir, b), "")
	}
	origExec := osExecutable
	osExecutable = func() (string, error) { return filepath.Join(binDir, "ai-agent"), nil }
	t.Cleanup(func() { osExecutable = origExec })

	var ran [][]string
	origRun := upRunCmd
	upRunCmd = func(c *exec.Cmd) error {
		ran = append(ran, c.Args)
		return nil
	}
	t.Cleanup(func() { upRunCmd = origRun })

	cmd := &cobra.Command{}
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	if err := launchProjectDevcontainer(cmd, "devcontainer", containerRuntimePodman, project); err != nil {
		t.Fatalf("launchProjectDevcontainer: %v", err)
	}

	if len(ran) != 2 {
		t.Fatalf("expected up then shell (2 commands), got %d: %v", len(ran), ran)
	}
	up := strings.Join(ran[0], " ")
	if !strings.Contains(up, "up ") || !strings.Contains(up, "--workspace-folder "+project) {
		t.Fatalf("first command is not a project up: %q", up)
	}
	if !strings.Contains(up, "target=/run/ai-agent,readonly") {
		t.Fatalf("up command missing read-only broker overlay: %q", up)
	}
	shell := ran[1]
	if shell[len(shell)-3] != "sh" || shell[len(shell)-2] != "-c" || shell[len(shell)-1] != fallbackShell {
		t.Fatalf("shell command does not use the bash/sh fallback: %v", shell)
	}
}

func TestLaunchProjectDevcontainerRejectsMissingDevcontainer(t *testing.T) {
	project := t.TempDir()
	cmd := &cobra.Command{}
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	if err := launchProjectDevcontainer(cmd, "devcontainer", containerRuntimePodman, project); err == nil {
		t.Fatal("expected error when the project has no .devcontainer")
	}
}

func TestBrokerOverlayArgsFailsWhenToolchainIncomplete(t *testing.T) {
	binDir := t.TempDir()
	mustWriteFile(t, filepath.Join(binDir, "ai-agent"), "")

	orig := osExecutable
	osExecutable = func() (string, error) { return filepath.Join(binDir, "ai-agent"), nil }
	t.Cleanup(func() { osExecutable = orig })

	if _, err := brokerOverlayArgs(); err == nil {
		t.Fatal("expected error when the ai-agent toolchain is incomplete")
	}
}

func mustWriteFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}
