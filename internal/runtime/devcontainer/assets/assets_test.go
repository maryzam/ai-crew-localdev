package assets

import (
	"encoding/json"
	"io/fs"
	"strings"
	"testing"

	"github.com/maryzam/ai-crew-localdev/internal/platform/embedasset"
)

const checkoutConfigDir = "../../../../.devcontainer"

func TestEmbeddedGenericAssetsMatchCheckout(t *testing.T) {
	generic, err := Generic()
	if err != nil {
		t.Fatal(err)
	}
	if err := embedasset.Parity(generic, checkoutConfigDir); err != nil {
		t.Fatalf("generic asset parity failed; run 'make devcontainer-assets': %v", err)
	}
}

func TestGenericImageBuildsFromStagedBinaryNotSource(t *testing.T) {
	generic, err := Generic()
	if err != nil {
		t.Fatal(err)
	}
	dockerfile, err := fs.ReadFile(generic, "Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	content := string(dockerfile)
	if !strings.Contains(content, "COPY bin/ai-agent /usr/local/bin/ai-agent") {
		t.Fatal("generic image must install the staged ai-agent binary from the build context")
	}
	for _, sourceBuild := range []string{"COPY . .", "COPY go.mod", "RUN make build", "go build"} {
		if strings.Contains(content, sourceBuild) {
			t.Fatalf("generic image must not build from a source checkout (found %q); a released binary has no source tree", sourceBuild)
		}
	}
}

func TestGenericDevcontainerDeclaresOnlyManagedMounts(t *testing.T) {
	config := genericDevcontainerConfig(t)
	mounts := stringSlice(t, config["mounts"])
	want := []string{
		"source=${localEnv:XDG_RUNTIME_DIR}/ai-agent,target=/run/ai-agent,type=bind",
		"source=ai-agent-home,target=/home/dev,type=volume",
	}
	if strings.Join(mounts, "\n") != strings.Join(want, "\n") {
		t.Fatalf("mounts = %#v, want %#v", mounts, want)
	}
	workspaceMount, ok := config["workspaceMount"].(string)
	if !ok {
		t.Fatalf("workspaceMount = %#v, want string", config["workspaceMount"])
	}
	if workspaceMount != "source=${localEnv:AI_AGENT_WORKSPACE},target=/workspace,type=bind" {
		t.Fatalf("workspaceMount = %q", workspaceMount)
	}
}

func TestGenericDevcontainerDeclaresConfinementArgs(t *testing.T) {
	config := genericDevcontainerConfig(t)
	args := stringSet(t, config["runArgs"])
	for _, want := range []string{
		"--cap-drop=ALL",
		"--security-opt=no-new-privileges",
		"--read-only",
		"--tmpfs=/tmp:rw,noexec,nosuid,size=512m",
	} {
		if !args[want] {
			t.Fatalf("runArgs missing %q in %#v", want, args)
		}
	}
}

func genericDevcontainerConfig(t *testing.T) map[string]any {
	t.Helper()
	generic, err := Generic()
	if err != nil {
		t.Fatal(err)
	}
	data, err := fs.ReadFile(generic, "devcontainer.json")
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	return config
}

func stringSlice(t *testing.T, value any) []string {
	t.Helper()
	raw, ok := value.([]any)
	if !ok {
		t.Fatalf("value = %#v, want []any", value)
	}
	result := make([]string, 0, len(raw))
	for _, item := range raw {
		text, ok := item.(string)
		if !ok {
			t.Fatalf("item = %#v, want string", item)
		}
		result = append(result, text)
	}
	return result
}

func stringSet(t *testing.T, value any) map[string]bool {
	t.Helper()
	result := map[string]bool{}
	for _, item := range stringSlice(t, value) {
		result[item] = true
	}
	return result
}
