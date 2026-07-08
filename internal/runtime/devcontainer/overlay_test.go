package devcontainer

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOverlayPreservesEnvironmentAndUsesReadOnlyMounts(t *testing.T) {
	builder, project := overlayFixture(t, `{"remoteEnv":{"PATH":"/project/bin","CUSTOM":"kept"},"mounts":["source=cache,target=/cache,type=volume"]}`)
	args, err := builder.Args(project)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(args[1])
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	env := config["remoteEnv"].(map[string]any)
	if env["CUSTOM"] != "kept" || env["AI_AGENT_AUTH_SOCK"] != "/run/ai-agent/broker.sock" || env["PATH"] != ContainerBinDir+":/project/bin" {
		t.Fatalf("remoteEnv = %#v", env)
	}
	mounts := config["mounts"].([]any)
	joined := fmt.Sprint(mounts)
	for _, expected := range []string{"target=/run/ai-agent", "target=" + ContainerBinDir + "/ai-agent", "readonly"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("mounts = %s, missing %s", joined, expected)
		}
	}
}

func TestOverlaySupportsJSONCAndReadOnlyComposeVolumes(t *testing.T) {
	builder, project := overlayFixture(t, "{\n// project config\n\"dockerComposeFile\": [\"compose.yml\",],\n\"service\": \"app\",\n}")
	args, err := builder.Args(project)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(args[1])
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	compose := config["dockerComposeFile"].([]any)
	overlayPath := compose[len(compose)-1].(string)
	overlay, err := os.ReadFile(overlayPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"/run/ai-agent:ro", ContainerBinDir + "/ai-agent:ro", ContainerBinDir + "/gh:ro"} {
		if !strings.Contains(string(overlay), expected) {
			t.Fatalf("compose overlay = %s, missing %s", overlay, expected)
		}
	}
}

func TestOverlayRejectsMissingBinary(t *testing.T) {
	builder, project := overlayFixture(t, `{}`)
	if err := os.Remove(mustExecutable(t, builder)); err != nil {
		t.Fatal(err)
	}
	if _, err := builder.Args(project); err == nil || !strings.Contains(err.Error(), "ai-agent binary not found") {
		t.Fatalf("error = %v", err)
	}
}

func TestOverlayMountsSingleBinaryAtEveryToolchainName(t *testing.T) {
	builder, project := overlayFixture(t, `{}`)
	binDir := filepath.Dir(mustExecutable(t, builder))
	for _, name := range []string{"ai-agent-gh", "ai-agent-credential-helper"} {
		if err := os.Remove(filepath.Join(binDir, name)); err != nil {
			t.Fatal(err)
		}
	}

	args, err := builder.Args(project)
	if err != nil {
		t.Fatalf("single-binary install must satisfy the toolchain: %v", err)
	}
	overlay, err := os.ReadFile(args[1])
	if err != nil {
		t.Fatal(err)
	}

	hostBinary := filepath.Join(binDir, "ai-agent")
	for _, name := range []string{"ai-agent", "ai-agent-gh", "ai-agent-credential-helper", "gh"} {
		mount := "source=" + hostBinary + ",target=" + ContainerBinDir + "/" + name + ",type=bind,readonly"
		if !strings.Contains(string(overlay), mount) {
			t.Fatalf("overlay %s missing mount of the single binary at %s", overlay, name)
		}
	}
}

func overlayFixture(t *testing.T, config string) (OverlayBuilder, string) {
	t.Helper()
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, ".devcontainer"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".devcontainer", "devcontainer.json"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	for _, name := range []string{"ai-agent", "ai-agent-gh", "ai-agent-credential-helper"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	builder := NewOverlayBuilder(func() (string, error) { return filepath.Join(bin, "ai-agent"), nil })
	return builder, project
}

func mustExecutable(t *testing.T, builder OverlayBuilder) string {
	t.Helper()
	path, err := builder.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return path
}
