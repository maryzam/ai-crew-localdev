package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/maryzam/ai-crew-localdev/internal/product/agents"
	"github.com/maryzam/ai-crew-localdev/internal/product/login"
)

func TestGreenfieldRootExposesOnlyLandedOperatorCommands(t *testing.T) {
	root := NewRoot(agents.Default(), &fakeLoginService{}, &bytes.Buffer{}, &bytes.Buffer{})
	commands := root.Commands()
	if len(commands) != 1 || commands[0].Name() != "login" {
		t.Fatalf("commands = %v", commands)
	}
}

func TestLoginReportsPublishedGenerationAndPinnedRefreshBinary(t *testing.T) {
	var out bytes.Buffer
	service := &fakeLoginService{credential: login.Credential{Agent: "codex", Generation: "0123456789abcdef", RefreshBinary: "/broker/bin/codex-0.163.0/codex", RefreshBinarySHA256: "abc"}}
	root := NewRoot(agents.Default(), service, &out, &bytes.Buffer{})
	root.SetArgs([]string{"login", "codex"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "generation 0123456789abcdef") || !strings.Contains(out.String(), "SHA-256 abc") {
		t.Fatalf("output = %q", out.String())
	}
}

func TestLoginReportsPublicationEvenWhenCleanupFails(t *testing.T) {
	var out bytes.Buffer
	service := &fakeLoginService{credential: login.Credential{Agent: "claude", Generation: "0123456789abcdef"}, err: errors.New("cleanup failed")}
	root := NewRoot(agents.Default(), service, &out, &bytes.Buffer{})
	root.SetArgs([]string{"login", "claude"})
	if err := root.Execute(); err == nil || !strings.Contains(out.String(), "generation 0123456789abcdef") {
		t.Fatalf("error = %v, output = %q", err, out.String())
	}
}

func TestLoginRefusesWithoutInteractiveTerminal(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	registry := agents.Default()
	service := login.New(registry, login.Console{In: strings.NewReader(""), Out: &bytes.Buffer{}, Err: &bytes.Buffer{}})
	root := NewRoot(registry, service, &bytes.Buffer{}, &bytes.Buffer{})
	root.SetArgs([]string{"login", "claude"})
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "interactive terminal") {
		t.Fatalf("non-terminal error = %v", err)
	}
}

type fakeLoginService struct {
	credential login.Credential
	err        error
}

func (f *fakeLoginService) Login(context.Context, string) (login.Credential, error) {
	return f.credential, f.err
}
