package cli

import (
	"encoding/json"
	"errors"
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
	project := t.TempDir()
	mustWriteFile(t, filepath.Join(project, ".devcontainer", "devcontainer.json"), `{"image":"ubuntu:24.04"}`)
	binDir := t.TempDir()
	runtimeDir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	for _, b := range injectedToolchainBinaries {
		mustWriteFile(t, filepath.Join(binDir, b), "")
	}
	fakeSelf := filepath.Join(binDir, "ai-agent")

	orig := osExecutable
	osExecutable = func() (string, error) { return fakeSelf, nil }
	t.Cleanup(func() { osExecutable = orig })

	args, err := brokerOverlayArgs(project)
	if err != nil {
		t.Fatalf("brokerOverlayArgs: %v", err)
	}

	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--override-config "+filepath.Join(runtimeDir, "ai-agent", "devcontainer-broker-overlay-")) {
		t.Fatalf("brokerOverlayArgs missing --override-config in %q", joined)
	}

	overlay := readOverlayConfig(t, args)
	if got := overlay["image"]; got != "ubuntu:24.04" {
		t.Fatalf("overlay did not preserve project image: %#v", got)
	}
	remoteEnv := overlayRemoteEnv(t, overlay)
	if got := remoteEnv["AI_AGENT_AUTH_SOCK"]; got != "/run/ai-agent/broker.sock" {
		t.Fatalf("remoteEnv AI_AGENT_AUTH_SOCK = %#v, want /run/ai-agent/broker.sock", got)
	}
	for _, key := range []string{"AI_AGENT_LANGFUSE_PUBLIC_KEY", "AI_AGENT_LANGFUSE_SECRET_KEY", "AI_AGENT_OTLP_TRACES_ENDPOINT", "AI_AGENT_OTLP_HEADERS"} {
		if got := remoteEnv[key]; got != "${localEnv:"+key+"}" {
			t.Errorf("remoteEnv %s = %#v", key, got)
		}
	}
	if got, _ := remoteEnv["PATH"].(string); got != "/usr/local/ai-agent/bin:${containerEnv:PATH}" {
		t.Fatalf("remoteEnv PATH = %#v, want bin prepended to ${containerEnv:PATH}", got)
	}
	assertOverlayMount(t, overlay, runtimeDir+"/ai-agent", "/run/ai-agent")
	assertOverlayMount(t, overlay, filepath.Join(binDir, "ai-agent-gh"), "/usr/local/ai-agent/bin/ai-agent-gh")
}

func TestBrokerOverlayArgsPreservesProjectRemoteEnvPath(t *testing.T) {
	project := t.TempDir()
	mustWriteFile(t, filepath.Join(project, ".devcontainer", "devcontainer.json"),
		`{"image":"ubuntu:24.04","remoteEnv":{"PATH":"/opt/project/bin:${containerEnv:PATH}","FOO":"bar"}}`)
	binDir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	for _, b := range injectedToolchainBinaries {
		mustWriteFile(t, filepath.Join(binDir, b), "")
	}
	orig := osExecutable
	osExecutable = func() (string, error) { return filepath.Join(binDir, "ai-agent"), nil }
	t.Cleanup(func() { osExecutable = orig })

	args, err := brokerOverlayArgs(project)
	if err != nil {
		t.Fatalf("brokerOverlayArgs: %v", err)
	}
	remoteEnv := overlayRemoteEnv(t, readOverlayConfig(t, args))
	if got, _ := remoteEnv["PATH"].(string); got != "/usr/local/ai-agent/bin:/opt/project/bin:${containerEnv:PATH}" {
		t.Fatalf("remoteEnv PATH = %#v, want toolchain prepended to the project's own PATH", got)
	}
	if got := remoteEnv["FOO"]; got != "bar" {
		t.Fatalf("remoteEnv dropped project key FOO: %#v", remoteEnv)
	}
}

func TestBrokerOverlayArgsMountsAreReadOnly(t *testing.T) {
	project := t.TempDir()
	mustWriteFile(t, filepath.Join(project, ".devcontainer", "devcontainer.json"), `{"image":"ubuntu:24.04"}`)
	binDir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	for _, b := range injectedToolchainBinaries {
		mustWriteFile(t, filepath.Join(binDir, b), "")
	}
	orig := osExecutable
	osExecutable = func() (string, error) { return filepath.Join(binDir, "ai-agent"), nil }
	t.Cleanup(func() { osExecutable = orig })

	args, err := brokerOverlayArgs(project)
	if err != nil {
		t.Fatalf("brokerOverlayArgs: %v", err)
	}

	overlay := readOverlayConfig(t, args)
	mounts := overlayMounts(t, overlay)
	if len(mounts) != len(injectedToolchainBinaries)+1 {
		t.Fatalf("overlay mounts = %d, want %d", len(mounts), len(injectedToolchainBinaries)+1)
	}
	for _, mount := range mounts {
		if !strings.Contains(mount, "readonly") {
			t.Fatalf("overlay mount not read-only: %#v", mount)
		}
	}
}

func TestBrokerOverlayInjectsUsageAdapterWhenInstalled(t *testing.T) {
	project := t.TempDir()
	mustWriteFile(t, filepath.Join(project, ".devcontainer", "devcontainer.json"), `{"image":"ubuntu:24.04"}`)
	binDir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	for _, binary := range append(injectedToolchainBinaries, optionalToolchainBinaries...) {
		mustWriteFile(t, filepath.Join(binDir, binary), "")
	}
	originalExecutable := osExecutable
	osExecutable = func() (string, error) { return filepath.Join(binDir, "ai-agent"), nil }
	t.Cleanup(func() { osExecutable = originalExecutable })

	args, err := brokerOverlayArgs(project)
	if err != nil {
		t.Fatal(err)
	}
	overlay := readOverlayConfig(t, args)
	assertOverlayMount(t, overlay, filepath.Join(binDir, "ccusage"), "/usr/local/ai-agent/bin/ccusage")
}

func TestLaunchProjectDevcontainerOrchestratesUpThenShell(t *testing.T) {
	project := t.TempDir()
	mustWriteFile(t, filepath.Join(project, ".devcontainer", "devcontainer.json"), "{}")
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	binDir := t.TempDir()
	for _, b := range injectedToolchainBinaries {
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

	if len(ran) != 3 {
		t.Fatalf("expected up, bootstrap, and shell (3 commands), got %d: %v", len(ran), ran)
	}
	up := strings.Join(ran[0], " ")
	if !strings.Contains(up, "up ") || !strings.Contains(up, "--workspace-folder "+project) {
		t.Fatalf("first command is not a project up: %q", up)
	}
	if !strings.Contains(up, "--override-config") {
		t.Fatalf("up command missing read-only broker overlay: %q", up)
	}
	bootstrap := strings.Join(ran[1], " ")
	if !strings.Contains(bootstrap, "/usr/local/ai-agent/bin/ai-agent bootstrap --quiet") {
		t.Fatalf("second command is not bootstrap: %q", bootstrap)
	}
	shell := ran[2]
	if shell[len(shell)-3] != "sh" || shell[len(shell)-2] != "-c" || shell[len(shell)-1] != fallbackShell {
		t.Fatalf("shell command does not use the bash/sh fallback: %v", shell)
	}
}

func TestLaunchProjectDevcontainerContinuesWhenBootstrapFails(t *testing.T) {
	project := t.TempDir()
	mustWriteFile(t, filepath.Join(project, ".devcontainer", "devcontainer.json"), "{}")
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	binDir := t.TempDir()
	for _, binary := range injectedToolchainBinaries {
		mustWriteFile(t, filepath.Join(binDir, binary), "")
	}
	originalExecutable := osExecutable
	osExecutable = func() (string, error) { return filepath.Join(binDir, "ai-agent"), nil }
	t.Cleanup(func() { osExecutable = originalExecutable })

	call := 0
	originalRun := upRunCmd
	upRunCmd = func(command *exec.Cmd) error {
		call++
		if call == 2 {
			return errors.New("bootstrap failed")
		}
		return nil
	}
	t.Cleanup(func() { upRunCmd = originalRun })
	cmd := &cobra.Command{}
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	if err := launchProjectDevcontainer(cmd, "devcontainer", containerRuntimePodman, project); err != nil {
		t.Fatalf("launch should continue: %v", err)
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
	project := t.TempDir()
	mustWriteFile(t, filepath.Join(project, ".devcontainer", "devcontainer.json"), `{"image":"ubuntu:24.04"}`)
	binDir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	mustWriteFile(t, filepath.Join(binDir, "ai-agent"), "")

	orig := osExecutable
	osExecutable = func() (string, error) { return filepath.Join(binDir, "ai-agent"), nil }
	t.Cleanup(func() { osExecutable = orig })

	if _, err := brokerOverlayArgs(project); err == nil {
		t.Fatal("expected error when the ai-agent toolchain is incomplete")
	}
}

func TestBrokerOverlayArgsParsesJSONCProjectConfig(t *testing.T) {
	project := t.TempDir()
	mustWriteFile(t, filepath.Join(project, ".devcontainer", "devcontainer.json"), `{
  // common in devcontainer.json files
  "image": "ubuntu:24.04",
  "containerEnv": {
    "URL": "https://example.test/not-a-comment",
  },
}`)
	binDir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	for _, b := range injectedToolchainBinaries {
		mustWriteFile(t, filepath.Join(binDir, b), "")
	}
	orig := osExecutable
	osExecutable = func() (string, error) { return filepath.Join(binDir, "ai-agent"), nil }
	t.Cleanup(func() { osExecutable = orig })

	args, err := brokerOverlayArgs(project)
	if err != nil {
		t.Fatalf("brokerOverlayArgs: %v", err)
	}
	overlay := readOverlayConfig(t, args)
	if got := overlay["image"]; got != "ubuntu:24.04" {
		t.Fatalf("overlay image = %#v", got)
	}
}

func TestBrokerOverlayArgsAppendsReadOnlyComposeOverlay(t *testing.T) {
	project := t.TempDir()
	mustWriteFile(t, filepath.Join(project, ".devcontainer", "devcontainer.json"), `{
  "dockerComposeFile": "compose.yml",
  "service": "app"
}`)
	binDir := t.TempDir()
	runtimeDir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	for _, b := range injectedToolchainBinaries {
		mustWriteFile(t, filepath.Join(binDir, b), "")
	}
	orig := osExecutable
	osExecutable = func() (string, error) { return filepath.Join(binDir, "ai-agent"), nil }
	t.Cleanup(func() { osExecutable = orig })

	args, err := brokerOverlayArgs(project)
	if err != nil {
		t.Fatalf("brokerOverlayArgs: %v", err)
	}
	overlay := readOverlayConfig(t, args)
	files, ok := overlay["dockerComposeFile"].([]any)
	if !ok || len(files) != 2 {
		t.Fatalf("dockerComposeFile = %#v, want original plus overlay", overlay["dockerComposeFile"])
	}
	composeOverlay, ok := files[1].(string)
	if !ok {
		t.Fatalf("compose overlay path = %#v", files[1])
	}
	data, err := os.ReadFile(composeOverlay)
	if err != nil {
		t.Fatalf("read compose overlay: %v", err)
	}
	content := string(data)
	for _, want := range []string{
		"'app':",
		runtimeDir + "/ai-agent:/run/ai-agent:ro",
		filepath.Join(binDir, "ai-agent-gh") + ":/usr/local/ai-agent/bin/ai-agent-gh:ro",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("compose overlay missing %q:\n%s", want, content)
		}
	}
	if _, hasMounts := overlay["mounts"]; hasMounts {
		t.Fatalf("compose-backed overlay should use compose volumes, got mounts: %#v", overlay["mounts"])
	}
}

func readOverlayConfig(t *testing.T, args []string) map[string]any {
	t.Helper()

	var path string
	for i, arg := range args {
		if arg == "--override-config" && i+1 < len(args) {
			path = args[i+1]
			break
		}
	}
	if path == "" {
		t.Fatalf("missing --override-config in %v", args)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read overlay config: %v", err)
	}
	var overlay map[string]any
	if err := json.Unmarshal(data, &overlay); err != nil {
		t.Fatalf("parse overlay config: %v", err)
	}
	return overlay
}

func overlayMounts(t *testing.T, overlay map[string]any) []string {
	t.Helper()

	rawMounts, ok := overlay["mounts"].([]any)
	if !ok {
		t.Fatalf("overlay missing mounts: %#v", overlay)
	}
	mounts := make([]string, 0, len(rawMounts))
	for _, raw := range rawMounts {
		mount, ok := raw.(string)
		if !ok {
			t.Fatalf("overlay mount is not string: %#v", raw)
		}
		mounts = append(mounts, mount)
	}
	return mounts
}

func overlayRemoteEnv(t *testing.T, overlay map[string]any) map[string]any {
	t.Helper()

	remoteEnv, ok := overlay["remoteEnv"].(map[string]any)
	if !ok {
		t.Fatalf("overlay missing remoteEnv: %#v", overlay)
	}
	return remoteEnv
}

func assertOverlayMount(t *testing.T, overlay map[string]any, source string, target string) {
	t.Helper()

	for _, mount := range overlayMounts(t, overlay) {
		if strings.Contains(mount, "source="+source) &&
			strings.Contains(mount, "target="+target) &&
			strings.Contains(mount, "type=bind") &&
			strings.Contains(mount, "readonly") {
			return
		}
	}
	t.Fatalf("missing read-only bind mount source=%s target=%s in %#v", source, target, overlay["mounts"])
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
