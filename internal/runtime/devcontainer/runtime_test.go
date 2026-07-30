package devcontainer

import (
	"reflect"
	"strings"
	"testing"
)

const landingWarn = " || echo 'ai-agent: could not enter the target repository, staying in the workspace root' >&2; exec bash"

func TestInteractiveShellEmptyReturnsPlainBash(t *testing.T) {
	if got := InteractiveShell(""); !reflect.DeepEqual(got, []string{"bash"}) {
		t.Fatalf("InteractiveShell(\"\") = %v, want [bash]", got)
	}
}

func TestInteractiveShellCdsThenExecsWithWarning(t *testing.T) {
	got := InteractiveShell("/workspace/my project")
	want := []string{"bash", "-c", "cd '/workspace/my project'" + landingWarn}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("InteractiveShell = %v, want %v", got, want)
	}
}

func TestInteractiveShellNeutralizesHostileDirName(t *testing.T) {
	got := InteractiveShell("/workspace/evil'; rm -rf ~; '")
	want := []string{"bash", "-c", `cd '/workspace/evil'\''; rm -rf ~; '\'''` + landingWarn}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("InteractiveShell = %q, want %q", got, want)
	}
}

func TestExecCommandArgsEmbedsLandingShell(t *testing.T) {
	got := ExecCommandArgs("/repo", Podman, InteractiveShell("/workspace/demo"))
	if !strings.Contains(got, "/workspace/demo") || !strings.Contains(got, "--workspace-folder /repo") {
		t.Fatalf("re-entry command should embed the landing shell, got %q", got)
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
