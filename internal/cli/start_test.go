package cli

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/maryzam/ai-crew-localdev/internal/app/startsession"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/identity"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/schema"
	"github.com/spf13/cobra"
)

func TestConfiguredStartAgentsResolveAliasesToCompiledExecutables(t *testing.T) {
	identities := &identity.IdentitiesFile{
		SchemaVersion: schema.IdentitiesSchemaV2,
		Agents: map[string]identity.AgentIdentity{
			"claude": {Tool: "claude-code", GitName: "Claude Bot", GitEmail: "claude@example.test"},
			"codex":  {Tool: "codex", GitName: "Codex Bot", GitEmail: "codex@example.test"},
		},
	}

	agents, err := configuredStartAgents(identities)
	if err != nil {
		t.Fatal(err)
	}
	want := []startsession.Agent{
		{Name: "claude", Tool: "claude", GitName: "Claude Bot", GitEmail: "claude@example.test"},
		{Name: "codex", Tool: "codex", GitName: "Codex Bot", GitEmail: "codex@example.test"},
	}
	if !reflect.DeepEqual(agents, want) {
		t.Fatalf("agents = %+v, want %+v", agents, want)
	}
}

func TestConfiguredStartAgentsUseCompiledDefaultAndRejectUnknownTools(t *testing.T) {
	tests := []struct {
		name  string
		agent string
		tool  string
		want  string
	}{
		{name: "compiled default", agent: "claude", want: "claude"},
		{name: "custom identity with compiled tool", agent: "reviewer", tool: "codex", want: "codex"},
		{name: "unknown alias", agent: "claude", tool: "claude-desktop", want: `agent "claude" configures unsupported tool "claude-desktop"`},
		{name: "custom identity without tool", agent: "custom", want: `agent "custom" must configure a supported tool explicitly`},
		{name: "unknown tool", agent: "custom", tool: "custom", want: `agent "custom" configures unsupported tool "custom"`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			identities := &identity.IdentitiesFile{
				SchemaVersion: schema.IdentitiesSchemaV2,
				Agents: map[string]identity.AgentIdentity{
					test.agent: {Tool: test.tool, GitName: "Agent Bot", GitEmail: "agent@example.test"},
				},
			}
			agents, err := configuredStartAgents(identities)
			if test.name == "compiled default" || test.name == "custom identity with compiled tool" {
				if err != nil || len(agents) != 1 || agents[0].Tool != test.want {
					t.Fatalf("agents = %+v, error = %v", agents, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestStartCommandPreservesRepositoryAgentSelectionAndAgentArguments(t *testing.T) {
	runner := &fakeStartUseCase{result: startsession.Result{
		Workspace:       startsession.Workspace{ID: "workspace-1"},
		WorkspaceResult: startsession.WorkspaceResult{ResultCommit: "abc123", Changed: true},
	}}
	var captured startOptions
	command := newStartCommandWithFactory(ProviderServices{}, func(_ *cobra.Command, options startOptions) startUseCase {
		captured = options
		return runner
	})
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	command.SetArgs([]string{"/src/repo", "--agent", "codex", "--runtime", "docker", "--new", "--observability=false", "-v", "--", "--model", "o3"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	want := startsession.Request{SourcePath: "/src/repo", AgentName: "codex", AgentArgs: []string{"--model", "o3"}, New: true}
	if !reflect.DeepEqual(runner.request, want) {
		t.Fatalf("request = %+v, want %+v", runner.request, want)
	}
	if captured.runtime != "docker" || !captured.new || captured.observability || !captured.verbose {
		t.Fatalf("options = %+v", captured)
	}
	for _, expected := range []string{"Workspace workspace-1 preserved result abc123", "ai-agent apply --workspace workspace-1"} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("output %q missing %q", output.String(), expected)
		}
	}
}

func TestStartCommandDefaultsToCurrentRepositoryAndSoleAgentArguments(t *testing.T) {
	runner := &fakeStartUseCase{result: startsession.Result{Workspace: startsession.Workspace{ID: "workspace-1"}, WorkspaceResult: startsession.WorkspaceResult{ResultCommit: "base", Changed: false}}}
	command := newStartCommandWithFactory(ProviderServices{}, func(*cobra.Command, startOptions) startUseCase { return runner })
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetArgs(nil)
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if runner.request.SourcePath != "." || len(runner.request.AgentArgs) != 0 {
		t.Fatalf("request = %+v", runner.request)
	}
	if !strings.Contains(output.String(), "completed without repository changes") {
		t.Fatalf("output = %q", output.String())
	}
}

func TestStartCommandRejectsAmbiguousPositionalsBeforeUseCase(t *testing.T) {
	runner := &fakeStartUseCase{}
	command := newStartCommandWithFactory(ProviderServices{}, func(*cobra.Command, startOptions) startUseCase { return runner })
	var output bytes.Buffer
	command.SetErr(&output)
	command.SetArgs([]string{"one", "two"})
	err := command.Execute()
	if err == nil || !strings.Contains(err.Error(), "at most one repository") {
		t.Fatalf("error = %v", err)
	}
	if runner.calls != 0 {
		t.Fatalf("start calls = %d", runner.calls)
	}
	if !strings.Contains(output.String(), "Start failed: start accepts at most one repository") {
		t.Fatalf("output = %q", output.String())
	}
}

func TestStartCommandRendersFlagFailure(t *testing.T) {
	command := newStartCommandWithFactory(ProviderServices{}, func(*cobra.Command, startOptions) startUseCase { return &fakeStartUseCase{} })
	var output bytes.Buffer
	command.SetErr(&output)
	command.SetArgs([]string{"--unknown"})
	if err := command.Execute(); err == nil || !strings.Contains(output.String(), "Start failed: unknown flag") {
		t.Fatalf("error output = %q", output.String())
	}
}

func TestStartCommandReportsRetainedWorkspaceOnFailure(t *testing.T) {
	runner := &fakeStartUseCase{result: startsession.Result{Workspace: startsession.Workspace{ID: "workspace-1"}}, err: errors.New("agent failed\x1b\nnext")}
	command := newStartCommandWithFactory(ProviderServices{}, func(*cobra.Command, startOptions) startUseCase { return runner })
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	command.SetArgs(nil)
	err := command.Execute()
	if !errors.Is(err, runner.err) {
		t.Fatalf("error = %v", err)
	}
	if !strings.Contains(output.String(), "retained for recovery") || !strings.Contains(output.String(), `agent failed\u{1b}\u{a}next`) || strings.ContainsAny(output.String(), "\x1b") {
		t.Fatalf("output = %q", output.String())
	}
}

type fakeStartUseCase struct {
	request startsession.Request
	result  startsession.Result
	err     error
	calls   int
}

func (useCase *fakeStartUseCase) Start(_ context.Context, request startsession.Request) (startsession.Result, error) {
	useCase.calls++
	useCase.request = request
	return useCase.result, useCase.err
}
