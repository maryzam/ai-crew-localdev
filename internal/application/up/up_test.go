package up

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestRunOrdersGenericStartup(t *testing.T) {
	var calls []string
	ports := &recordingPorts{calls: &calls}
	useCase := newTestUseCase(t, ports)

	result, err := useCase.Run(context.Background(), Input{Workspace: "/workspace", Runtime: "podman", Build: true, Langfuse: true})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"workspace", "host-readiness", "configuration", "observability", "broker", "readiness", "devcontainer", "generic-root", "generic-launch"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	if result.Workspace != "/resolved" || result.Target != "/generic" || result.Runtime != "podman" || result.Project {
		t.Fatalf("result = %#v", result)
	}
}

func TestRunOrdersProjectStartupWithoutGenericDiscovery(t *testing.T) {
	var calls []string
	ports := &recordingPorts{calls: &calls}
	useCase := newTestUseCase(t, ports)

	result, err := useCase.Run(context.Background(), Input{Project: "/project", Runtime: "docker"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"workspace", "host-readiness", "configuration", "broker", "readiness", "devcontainer", "project-launch"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
	if result.Target != "/resolved" || result.Runtime != "docker" || !result.Project {
		t.Fatalf("result = %#v", result)
	}
}

func TestRunStopsAtEveryFailedStage(t *testing.T) {
	stages := []string{"workspace", "host-readiness", "configuration", "observability", "broker", "readiness", "devcontainer", "generic-root", "generic-launch"}
	for _, failedStage := range stages {
		t.Run(failedStage, func(t *testing.T) {
			var calls []string
			ports := &recordingPorts{calls: &calls, failedStage: failedStage}
			_, err := newTestUseCase(t, ports).Run(context.Background(), Input{Runtime: "podman", Langfuse: true})
			if err == nil {
				t.Fatal("expected error")
			}
			if calls[len(calls)-1] != failedStage {
				t.Fatalf("calls continued after failure: %v", calls)
			}
		})
	}
}

func TestRunStopsAtProjectLaunchFailure(t *testing.T) {
	var calls []string
	ports := &recordingPorts{calls: &calls, failedStage: "project-launch"}
	_, err := newTestUseCase(t, ports).Run(context.Background(), Input{Project: "/project", Runtime: "podman"})
	if err == nil {
		t.Fatal("expected error")
	}
	want := []string{"workspace", "host-readiness", "configuration", "broker", "readiness", "devcontainer", "project-launch"}
	if !reflect.DeepEqual(calls, want) {
		t.Fatalf("calls = %v, want %v", calls, want)
	}
}

func TestNewRejectsMissingPorts(t *testing.T) {
	if _, err := New(Ports{}); err == nil {
		t.Fatal("expected missing ports to fail")
	}
}

type recordingPorts struct {
	calls       *[]string
	failedStage string
}

func newTestUseCase(t *testing.T, ports *recordingPorts) UseCase {
	t.Helper()
	useCase, err := New(Ports{Workspace: ports, Readiness: ports, Setup: ports, Observability: ports, Broker: ports, Container: ports})
	if err != nil {
		t.Fatal(err)
	}
	return useCase
}

func (p *recordingPorts) fail(stage string) error {
	*p.calls = append(*p.calls, stage)
	if stage == p.failedStage {
		return errors.New("failed")
	}
	return nil
}

func (p *recordingPorts) Prepare(context.Context, Input) (string, error) {
	return "/resolved", p.fail("workspace")
}

func (p *recordingPorts) EnsureHost(_ context.Context, runtime string) (string, error) {
	return runtime, p.fail("host-readiness")
}

func (p *recordingPorts) EnsureManaged(_ context.Context, runtime string) (string, error) {
	return runtime, p.fail("readiness")
}

func (p *recordingPorts) EnsureConfigured(context.Context) error {
	return p.fail("configuration")
}

func (p *recordingPorts) Start(context.Context) error {
	return p.fail("observability")
}

func (p *recordingPorts) EnsureRunning(context.Context) error {
	return p.fail("broker")
}

func (p *recordingPorts) FindCLI(context.Context) (string, error) {
	return "/bin/devcontainer", p.fail("devcontainer")
}

func (p *recordingPorts) FindGenericRoot(context.Context) (string, error) {
	return "/generic", p.fail("generic-root")
}

func (p *recordingPorts) LaunchGeneric(context.Context, LaunchInput) error {
	return p.fail("generic-launch")
}

func (p *recordingPorts) LaunchProject(context.Context, LaunchInput) error {
	return p.fail("project-launch")
}
