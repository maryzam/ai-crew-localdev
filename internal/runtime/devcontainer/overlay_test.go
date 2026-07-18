package devcontainer

import (
	"encoding/json"
	"fmt"
	"github.com/maryzam/ai-crew-localdev/internal/platform/paths"
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

func TestOverlayAppliesManifestPortsCachesAndObservability(t *testing.T) {
	builder, project := overlayFixture(t, `{"remoteEnv":{"CUSTOM":"kept"},"forwardPorts":[3000]}`)
	writeOverlayManifest(t, project, `{"schema_version":"ai-agent-manifest/v2","resources":[{"uri":"langfuse:project:project-1"}],"caches":[{"name":"go-build","target":"/workspace/.cache/go-build"}],"ports":[{"number":8080}],"run_modes":["project_devcontainer"]}`)

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
	if env["AI_AGENT_OBSERVABILITY_RESOURCE"] != "langfuse:project:project-1" {
		t.Fatalf("remoteEnv = %#v", env)
	}
	mounts := fmt.Sprint(config["mounts"])
	if !strings.Contains(mounts, "target=/workspace/.cache/go-build,type=volume") {
		t.Fatalf("mounts = %s", mounts)
	}
	ports := fmt.Sprint(config["forwardPorts"])
	if !strings.Contains(ports, "3000") || !strings.Contains(ports, "8080") {
		t.Fatalf("forwardPorts = %s", ports)
	}
}

func TestOverlayAppliesManifestComposeServicesAndCaches(t *testing.T) {
	builder, project := overlayFixture(t, `{"dockerComposeFile":"compose.yml","service":"app","runServices":["app"]}`)
	writeOverlayManifest(t, project, `{"schema_version":"ai-agent-manifest/v2","caches":[{"name":"npm","target":"/workspace/.npm","read_only":true}],"services":[{"name":"helper"}],"ports":[{"number":8080}],"run_modes":["project_devcontainer"]}`)

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
	runServices := fmt.Sprint(config["runServices"])
	if !strings.Contains(runServices, "app") || !strings.Contains(runServices, "helper") {
		t.Fatalf("runServices = %s", runServices)
	}
	ports := fmt.Sprint(config["forwardPorts"])
	if !strings.Contains(ports, "8080") {
		t.Fatalf("forwardPorts = %s", ports)
	}
	compose := config["dockerComposeFile"].([]any)
	overlayPath := compose[len(compose)-1].(string)
	overlay, err := os.ReadFile(overlayPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{":/workspace/.npm:ro", "volumes:", "ai-agent-cache-"} {
		if !strings.Contains(string(overlay), expected) {
			t.Fatalf("compose overlay = %s, missing %s", overlay, expected)
		}
	}
}

func TestOverlayRejectsManifestRunModeAndReservedCaches(t *testing.T) {
	builder, project := overlayFixture(t, `{}`)
	writeOverlayManifest(t, project, `{"schema_version":"ai-agent-manifest/v2","run_modes":["managed_run"]}`)
	if _, err := builder.Args(project); err == nil || !strings.Contains(err.Error(), "does not allow run mode") {
		t.Fatalf("error = %v, want run mode refusal", err)
	}

	builder, project = overlayFixture(t, `{}`)
	writeOverlayManifest(t, project, `{"schema_version":"ai-agent-manifest/v2","caches":[{"name":"broker","target":"/run/ai-agent/cache"}]}`)
	if _, err := builder.Args(project); err == nil || !strings.Contains(err.Error(), "reserved ai-agent path") {
		t.Fatalf("error = %v, want reserved cache refusal", err)
	}
}

func TestOverlayRejectsCachesThatShadowReservedParents(t *testing.T) {
	for _, target := range []string{"/usr/local/ai-agent", "/usr/local", "/run", "/"} {
		t.Run(target, func(t *testing.T) {
			builder, project := overlayFixture(t, `{}`)
			writeOverlayManifest(t, project, fmt.Sprintf(`{"schema_version":"ai-agent-manifest/v2","caches":[{"name":"shadow","target":%q}]}`, target))
			if _, err := builder.Args(project); err == nil || !strings.Contains(err.Error(), "reserved ai-agent path") {
				t.Fatalf("error = %v, want reserved cache refusal", err)
			}
		})
	}
}

func TestOverlayRejectsSanitizedCacheVolumeCollisions(t *testing.T) {
	builder, project := overlayFixture(t, `{}`)
	writeOverlayManifest(t, project, `{"schema_version":"ai-agent-manifest/v2","caches":[{"name":"go/build","target":"/workspace/a"},{"name":"go-build","target":"/workspace/b"}]}`)
	if _, err := builder.Args(project); err == nil || !strings.Contains(err.Error(), "same volume name") {
		t.Fatalf("error = %v, want sanitized cache volume collision", err)
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

func writeOverlayManifest(t *testing.T, project string, content string) {
	t.Helper()
	dir := filepath.Join(project, ".ai-agent")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustExecutable(t *testing.T, builder OverlayBuilder) string {
	t.Helper()
	path, err := builder.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func TestOverlayFollowsTheConfiguredBrokerSocket(t *testing.T) {
	builder, project := overlayFixture(t, `{}`)
	socketDir := t.TempDir()
	t.Setenv(paths.EnvBrokerSocket, filepath.Join(socketDir, "custom.sock"))

	args, err := builder.Args(project)
	if err != nil {
		t.Fatalf("Args: %v", err)
	}
	data, err := os.ReadFile(args[1])
	if err != nil {
		t.Fatal(err)
	}
	config := string(data)
	if !strings.Contains(config, "source="+socketDir+",target=/run/ai-agent") {
		t.Fatalf("overlay does not mount the configured socket directory:\n%s", config)
	}
	if !strings.Contains(config, `"AI_AGENT_AUTH_SOCK": "/run/ai-agent/custom.sock"`) {
		t.Fatalf("container clients are not pointed at the configured socket name:\n%s", config)
	}

	t.Setenv(paths.EnvBrokerSocket, "relative/custom.sock")
	if _, err := builder.Args(project); err == nil {
		t.Fatal("overlay accepted a relative broker socket")
	}
}
