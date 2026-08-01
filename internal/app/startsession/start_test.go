package startsession

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestStartSelectsExplicitAgentAndRunsPrivateWorkspaceLifecycle(t *testing.T) {
	var events []string
	configuration := &fakeAgentConfiguration{agents: []Agent{{Name: "claude", Tool: "claude", GitName: "Claude Bot", GitEmail: "claude@example.test"}, {Name: "codex", Tool: "codex", GitName: "Codex Bot", GitEmail: "codex@example.test"}}}
	workspaces := &fakeWorkspaceManager{
		workspace: Workspace{ID: "workspace-1", SourceRoot: "/src/repo", CheckoutPath: "/data/workspaces/workspace-1/checkout", BaseCommit: "abc123"},
		result:    WorkspaceResult{ResultCommit: "def456", Changed: true},
		lease:     Lease{ID: "lease-1"},
		events:    &events,
	}
	launcher := &fakeContainerLauncher{events: &events}
	useCase := New(configuration, workspaces, launcher)

	result, err := useCase.Start(context.Background(), Request{
		SourcePath: "/src/repo/nested",
		AgentName:  "codex",
		AgentArgs:  []string{"--model", "o3"},
		New:        true,
	})
	if err != nil {
		t.Fatal(err)
	}

	if workspaces.request != (WorkspaceRequest{SourcePath: "/src/repo/nested", New: true, AgentName: "codex", Tool: "codex"}) {
		t.Fatalf("workspace request = %+v", workspaces.request)
	}
	wantLaunch := LaunchRequest{
		WorkspaceID:  "workspace-1",
		CheckoutPath: "/data/workspaces/workspace-1/checkout",
		Argv:         []string{"ai-agent", "run", "--agent", "codex", "--repo", "/workspace", "--", "codex", "--model", "o3"},
	}
	if !reflect.DeepEqual(launcher.request, wantLaunch) {
		t.Fatalf("launch request = %+v, want %+v", launcher.request, wantLaunch)
	}
	if !reflect.DeepEqual(events, []string{"preflight", "prepare", "acquire", "launch", "finalize"}) {
		t.Fatalf("events = %v", events)
	}
	wantFinalize := FinalizeRequest{Workspace: workspaces.workspace, Lease: Lease{ID: "lease-1"}, GitName: "Codex Bot", GitEmail: "codex@example.test", Outcome: ExecutionSucceeded}
	if workspaces.finalizeRequest != wantFinalize {
		t.Fatalf("finalize request = %+v, want %+v", workspaces.finalizeRequest, wantFinalize)
	}
	if result.Agent != (Agent{Name: "codex", Tool: "codex", GitName: "Codex Bot", GitEmail: "codex@example.test"}) || result.Workspace != workspaces.workspace || result.WorkspaceResult != workspaces.result {
		t.Fatalf("result = %+v", result)
	}
}

func TestStartSelectsSoleConfiguredAgent(t *testing.T) {
	configuration := &fakeAgentConfiguration{agents: []Agent{{Name: "claude", Tool: "claude", GitName: "Claude Bot", GitEmail: "claude@example.test"}}}
	workspaces := &fakeWorkspaceManager{workspace: Workspace{ID: "workspace-1", CheckoutPath: "/private/checkout"}}
	launcher := &fakeContainerLauncher{}

	result, err := New(configuration, workspaces, launcher).Start(context.Background(), Request{SourcePath: "/src/repo"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Agent.Name != "claude" || !reflect.DeepEqual(launcher.request.Argv, []string{"ai-agent", "run", "--agent", "claude", "--repo", "/workspace", "--", "claude"}) {
		t.Fatalf("result = %+v, argv = %v", result, launcher.request.Argv)
	}
}

func TestStartFailsClosedBeforePreparingWorkspace(t *testing.T) {
	tests := []struct {
		name      string
		agents    []Agent
		requested string
		want      string
	}{
		{name: "none configured", want: "no agents are configured"},
		{name: "ambiguous", agents: []Agent{{Name: "claude", Tool: "claude", GitName: "Claude Bot", GitEmail: "claude@example.test"}, {Name: "codex", Tool: "codex", GitName: "Codex Bot", GitEmail: "codex@example.test"}}, want: "multiple agents are configured; pass --agent NAME"},
		{name: "unknown explicit", agents: []Agent{{Name: "codex", Tool: "codex", GitName: "Codex Bot", GitEmail: "codex@example.test"}}, requested: "claude", want: `agent "claude" is not configured`},
		{name: "empty name", agents: []Agent{{Name: "", Tool: "codex"}}, want: "configured agent has an empty name"},
		{name: "empty tool", agents: []Agent{{Name: "codex", Tool: " ", GitName: "Codex Bot", GitEmail: "codex@example.test"}}, want: `agent "codex" has an empty tool`},
		{name: "incomplete git identity", agents: []Agent{{Name: "codex", Tool: "codex"}}, want: `agent "codex" has an incomplete git identity`},
		{name: "duplicate", agents: []Agent{{Name: "codex", Tool: "codex", GitName: "Codex Bot", GitEmail: "codex@example.test"}, {Name: "codex", Tool: "codex", GitName: "Codex Bot", GitEmail: "codex@example.test"}}, requested: "codex", want: `agent "codex" is configured more than once`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			workspaces := &fakeWorkspaceManager{}
			launcher := &fakeContainerLauncher{}
			_, err := New(&fakeAgentConfiguration{agents: test.agents}, workspaces, launcher).Start(context.Background(), Request{SourcePath: "/src/repo", AgentName: test.requested})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
			if workspaces.prepareCalls != 0 || workspaces.acquireCalls != 0 || launcher.calls != 0 || workspaces.finalizeCalls != 0 {
				t.Fatalf("calls = prepare %d, acquire %d, launch %d, finalize %d", workspaces.prepareCalls, workspaces.acquireCalls, launcher.calls, workspaces.finalizeCalls)
			}
		})
	}
}

func TestStartReturnsConfigurationAndPreparationFailures(t *testing.T) {
	configurationErr := errors.New("configuration unavailable")
	workspaces := &fakeWorkspaceManager{}
	_, err := New(&fakeAgentConfiguration{err: configurationErr}, workspaces, &fakeContainerLauncher{}).Start(context.Background(), Request{SourcePath: "/src/repo"})
	if !errors.Is(err, configurationErr) || workspaces.prepareCalls != 0 {
		t.Fatalf("configuration error = %v, prepare calls = %d", err, workspaces.prepareCalls)
	}

	prepareErr := errors.New("workspace unavailable")
	workspaces = &fakeWorkspaceManager{prepareErr: prepareErr}
	launcher := &fakeContainerLauncher{}
	_, err = New(&fakeAgentConfiguration{agents: []Agent{{Name: "codex", Tool: "codex", GitName: "Codex Bot", GitEmail: "codex@example.test"}}}, workspaces, launcher).Start(context.Background(), Request{SourcePath: "/src/repo"})
	if !errors.Is(err, prepareErr) || workspaces.acquireCalls != 0 || launcher.calls != 0 || workspaces.finalizeCalls != 0 {
		t.Fatalf("prepare error = %v, acquire calls = %d, launch calls = %d, finalize calls = %d", err, workspaces.acquireCalls, launcher.calls, workspaces.finalizeCalls)
	}
}

func TestStartPreflightFailureStopsBeforeConfiguration(t *testing.T) {
	preflightErr := errors.New("source is dirty")
	configuration := &fakeAgentConfiguration{agents: []Agent{{Name: "codex", Tool: "codex", GitName: "Codex Bot", GitEmail: "codex@example.test"}}}
	workspaces := &fakeWorkspaceManager{preflightErr: preflightErr}
	_, err := New(configuration, workspaces, &fakeContainerLauncher{}).Start(context.Background(), Request{SourcePath: "/src/repo", New: true})
	if !errors.Is(err, preflightErr) || configuration.calls != 0 || workspaces.prepareCalls != 0 {
		t.Fatalf("error = %v, configuration calls = %d, prepare calls = %d", err, configuration.calls, workspaces.prepareCalls)
	}
	if workspaces.request != (WorkspaceRequest{SourcePath: "/src/repo", New: true}) {
		t.Fatalf("preflight request = %+v", workspaces.request)
	}
}

func TestStartRequiresExclusiveLeaseBeforeLaunch(t *testing.T) {
	leaseErr := errors.New("workspace already running")
	workspaces := &fakeWorkspaceManager{
		workspace:  Workspace{ID: "workspace-1", CheckoutPath: "/private/checkout"},
		acquireErr: leaseErr,
	}
	launcher := &fakeContainerLauncher{}

	result, err := New(&fakeAgentConfiguration{agents: []Agent{{Name: "codex", Tool: "codex", GitName: "Codex Bot", GitEmail: "codex@example.test"}}}, workspaces, launcher).Start(context.Background(), Request{SourcePath: "/src/repo"})
	if !errors.Is(err, leaseErr) {
		t.Fatalf("error = %v, want lease error", err)
	}
	if result.Workspace.ID != "workspace-1" || workspaces.acquireRequest != workspaces.workspace {
		t.Fatalf("result = %+v, acquire request = %+v", result, workspaces.acquireRequest)
	}
	if launcher.calls != 0 || workspaces.finalizeCalls != 0 {
		t.Fatalf("launch calls = %d, finalize calls = %d", launcher.calls, workspaces.finalizeCalls)
	}
}

func TestStartAlwaysFinalizesAndPreservesLaunchAndFinalizeFailures(t *testing.T) {
	launchErr := errors.New("container failed")
	finalizeErr := errors.New("checkpoint failed")
	ctx, cancel := context.WithCancel(context.Background())
	workspaces := &fakeWorkspaceManager{
		workspace:   Workspace{ID: "workspace-1", CheckoutPath: "/private/checkout"},
		lease:       Lease{ID: "lease-1"},
		result:      WorkspaceResult{ResultCommit: "preserved-result"},
		finalizeErr: finalizeErr,
	}
	launcher := &fakeContainerLauncher{err: launchErr, afterLaunch: cancel}

	result, err := New(&fakeAgentConfiguration{agents: []Agent{{Name: "codex", Tool: "codex", GitName: "Codex Bot", GitEmail: "codex@example.test"}}}, workspaces, launcher).Start(ctx, Request{SourcePath: "/src/repo"})
	if !errors.Is(err, launchErr) || !errors.Is(err, finalizeErr) {
		t.Fatalf("error = %v", err)
	}
	if workspaces.finalizeCalls != 1 || workspaces.finalizeContextErr != nil {
		t.Fatalf("finalize calls = %d, context error = %v", workspaces.finalizeCalls, workspaces.finalizeContextErr)
	}
	if workspaces.finalizeRequest.Lease != workspaces.lease || workspaces.finalizeRequest.Outcome != ExecutionCanceled {
		t.Fatalf("finalize request = %+v", workspaces.finalizeRequest)
	}
	if result.Workspace.ID != "workspace-1" || result.WorkspaceResult.ResultCommit != "preserved-result" {
		t.Fatalf("result = %+v", result)
	}
}

func TestExecutionOutcomeIsDeterministic(t *testing.T) {
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, test := range []struct {
		name      string
		ctx       context.Context
		launchErr error
		want      ExecutionOutcome
	}{
		{name: "succeeded", ctx: canceled, want: ExecutionSucceeded},
		{name: "failed", ctx: context.Background(), launchErr: errors.New("agent failed"), want: ExecutionFailed},
		{name: "canceled", ctx: canceled, launchErr: context.Canceled, want: ExecutionCanceled},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := executionOutcome(test.ctx, test.launchErr); got != test.want {
				t.Fatalf("execution outcome = %q, want %q", got, test.want)
			}
		})
	}
}

func TestStartRejectsMissingDependencies(t *testing.T) {
	_, err := New(nil, nil, nil).Start(context.Background(), Request{})
	if err == nil || !strings.Contains(err.Error(), "dependencies are not configured") {
		t.Fatalf("error = %v", err)
	}
}

type fakeAgentConfiguration struct {
	agents []Agent
	err    error
	calls  int
}

func (f *fakeAgentConfiguration) ConfiguredAgents(context.Context) ([]Agent, error) {
	f.calls++
	return append([]Agent(nil), f.agents...), f.err
}

type fakeWorkspaceManager struct {
	request            WorkspaceRequest
	workspace          Workspace
	lease              Lease
	result             WorkspaceResult
	prepareErr         error
	preflightErr       error
	acquireErr         error
	finalizeErr        error
	prepareCalls       int
	acquireCalls       int
	finalizeCalls      int
	acquireRequest     Workspace
	finalizeContextErr error
	finalizeRequest    FinalizeRequest
	events             *[]string
}

func (f *fakeWorkspaceManager) Preflight(_ context.Context, request WorkspaceRequest) error {
	f.request = request
	if f.events != nil {
		*f.events = append(*f.events, "preflight")
	}
	return f.preflightErr
}

func (f *fakeWorkspaceManager) Acquire(_ context.Context, workspace Workspace) (Lease, error) {
	f.acquireCalls++
	f.acquireRequest = workspace
	if f.events != nil {
		*f.events = append(*f.events, "acquire")
	}
	if f.lease.ID == "" {
		f.lease = Lease{ID: "lease-1"}
	}
	return f.lease, f.acquireErr
}

func (f *fakeWorkspaceManager) PrepareOrResume(_ context.Context, request WorkspaceRequest) (Workspace, error) {
	f.prepareCalls++
	f.request = request
	if f.events != nil {
		*f.events = append(*f.events, "prepare")
	}
	return f.workspace, f.prepareErr
}

func (f *fakeWorkspaceManager) Finalize(ctx context.Context, request FinalizeRequest) (WorkspaceResult, error) {
	f.finalizeCalls++
	f.finalizeContextErr = ctx.Err()
	f.finalizeRequest = request
	if f.events != nil {
		*f.events = append(*f.events, "finalize")
	}
	if request.Workspace != f.workspace {
		return WorkspaceResult{}, errors.New("wrong workspace finalized")
	}
	return f.result, f.finalizeErr
}

type fakeContainerLauncher struct {
	request     LaunchRequest
	err         error
	calls       int
	afterLaunch func()
	events      *[]string
}

func (f *fakeContainerLauncher) Launch(_ context.Context, request LaunchRequest) error {
	f.calls++
	f.request = request
	if f.events != nil {
		*f.events = append(*f.events, "launch")
	}
	if f.afterLaunch != nil {
		f.afterLaunch()
	}
	return f.err
}
