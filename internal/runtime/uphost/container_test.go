package uphost

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/maryzam/ai-crew-localdev/internal/runtime/devcontainer"
)

type recordedCommand struct {
	name string
	args []string
}

func TestContainerLauncherContinuesAfterOptionalProjectBootstrapFailure(t *testing.T) {
	project := t.TempDir()
	if err := os.MkdirAll(filepath.Join(project, ".devcontainer"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".devcontainer", "devcontainer.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	for _, name := range []string{"ai-agent", "ai-agent-gh", "ai-agent-credential-helper"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	runner := &recordingRunner{failAt: 2}
	var progress []Progress
	launcher := NewContainerLauncher(Streams{Out: io.Discard, Err: io.Discard}, ProgressFunc(func(value Progress) { progress = append(progress, value) }))
	launcher.Runner = runner.Run
	launcher.Overlay = devcontainer.NewOverlayBuilder(func() (string, error) { return filepath.Join(bin, "ai-agent"), nil })
	if err := launcher.LaunchProject(context.Background(), "devcontainer", project, "podman", false); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 4 {
		t.Fatalf("commands = %v", runner.commands)
	}
	foundWarning := false
	for _, event := range progress {
		foundWarning = foundWarning || event.Kind == ProjectBootstrapFailed
	}
	if !foundWarning {
		t.Fatalf("progress = %v", progress)
	}
}

type recordingRunner struct {
	commands []recordedCommand
	failAt   int
}

func (r *recordingRunner) Run(_ context.Context, name string, args []string, _ Streams) error {
	r.commands = append(r.commands, recordedCommand{name: name, args: append([]string(nil), args...)})
	if r.failAt == len(r.commands) {
		return errors.New("failed")
	}
	return nil
}

func TestContainerLauncherPreservesGenericCommandArguments(t *testing.T) {
	runner := &recordingRunner{}
	launcher := NewContainerLauncher(Streams{In: nil, Out: io.Discard, Err: io.Discard}, nil)
	launcher.Runner = runner.Run
	if err := launcher.LaunchGeneric(context.Background(), "/bin/devcontainer", "/host", "/repo", "podman", true); err != nil {
		t.Fatal(err)
	}
	want := []recordedCommand{
		{name: "/bin/devcontainer", args: []string{"up", "--docker-path", "podman", "--workspace-folder", "/repo", "--build-no-cache"}},
		{name: "/bin/devcontainer", args: []string{"exec", "--docker-path", "podman", "--workspace-folder", "/repo", "/usr/local/ai-agent/bin/ai-agent", "auth", "status"}},
		{name: "/bin/devcontainer", args: []string{"exec", "--docker-path", "podman", "--workspace-folder", "/repo", "bash"}},
	}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("commands = %v, want %v", runner.commands, want)
	}
}

func TestContainerLauncherStopsAfterFailedUp(t *testing.T) {
	runner := &recordingRunner{failAt: 1}
	launcher := NewContainerLauncher(Streams{Out: io.Discard, Err: io.Discard}, nil)
	launcher.Runner = runner.Run
	err := launcher.LaunchGeneric(context.Background(), "devcontainer", "", "/repo", "podman", false)
	if err == nil || err.Error() != "devcontainer up: failed" {
		t.Fatalf("error = %v", err)
	}
	if len(runner.commands) != 1 {
		t.Fatalf("commands = %d, want 1", len(runner.commands))
	}
}

func TestContainerLauncherReportsStableProgressContract(t *testing.T) {
	runner := &recordingRunner{}
	var progress []Progress
	launcher := NewContainerLauncher(Streams{Out: io.Discard, Err: io.Discard}, ProgressFunc(func(value Progress) { progress = append(progress, value) }))
	launcher.Runner = runner.Run
	if err := launcher.LaunchGeneric(context.Background(), "devcontainer", "/host", "/repo", "docker", false); err != nil {
		t.Fatal(err)
	}
	kinds := []ProgressKind{GenericLaunching, GenericReady, AuthStatusChecking, ShellOpening}
	if len(progress) != len(kinds) {
		t.Fatalf("progress count = %d, want %d", len(progress), len(kinds))
	}
	for index, kind := range kinds {
		if progress[index].Kind != kind {
			t.Fatalf("progress[%d] = %s, want %s", index, progress[index].Kind, kind)
		}
	}
	if progress[1].Command != "devcontainer exec --docker-path docker --workspace-folder /repo bash" {
		t.Fatalf("re-entry command = %q", progress[1].Command)
	}
}

func TestContainerLauncherWarnsAndContinuesWhenAuthStatusFails(t *testing.T) {
	runner := &recordingRunner{failAt: 2}
	var progress []Progress
	launcher := NewContainerLauncher(Streams{Out: io.Discard, Err: io.Discard}, ProgressFunc(func(value Progress) { progress = append(progress, value) }))
	launcher.Runner = runner.Run
	if err := launcher.LaunchGeneric(context.Background(), "devcontainer", "/host", "/repo", "podman", false); err != nil {
		t.Fatal(err)
	}
	if len(runner.commands) != 3 {
		t.Fatalf("commands = %v", runner.commands)
	}
	foundWarning := false
	for _, event := range progress {
		foundWarning = foundWarning || event.Kind == AuthStatusFailed
	}
	if !foundWarning {
		t.Fatalf("progress = %v", progress)
	}
}
