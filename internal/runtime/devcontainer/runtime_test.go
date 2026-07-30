package devcontainer

import (
	"reflect"
	"strings"
	"testing"
)

func TestInteractiveShellInDirCdsThenExecs(t *testing.T) {
	got := InteractiveShellInDir("/workspace/my project")
	want := []string{"bash", "-c", "cd '/workspace/my project' 2>/dev/null; exec bash"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("InteractiveShellInDir = %v, want %v", got, want)
	}
}

func TestRuntimeCommandsPreserveArguments(t *testing.T) {
	if got, want := UpArgs(Podman, "/repo", []string{"--override-config", "/tmp/overlay.json"}, true), []string{"up", "--docker-path", "podman", "--workspace-folder", "/repo", "--override-config", "/tmp/overlay.json", "--build-no-cache"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("up args = %v, want %v", got, want)
	}
	if got := ExecCommand("/home/me/my project", Docker); got != "devcontainer exec --docker-path docker --workspace-folder '/home/me/my project' bash" {
		t.Fatalf("exec command = %q", got)
	}
	for _, expected := range []string{"--override-config '/tmp/with space.json'", "sh -c", "exec bash"} {
		if got := ExecShellCommand("/repo", Podman, []string{"--override-config", "/tmp/with space.json"}); !strings.Contains(got, expected) {
			t.Fatalf("shell command = %q, missing %q", got, expected)
		}
	}
}
