package agents

import "testing"

func TestRegistryDeclaresEveryLoginContract(t *testing.T) {
	want := map[string]LoginKind{"claude": Claude, "codex": Codex}
	for _, agent := range All() {
		kind, ok := want[agent.Name]
		if !ok {
			t.Fatalf("unexpected agent %q", agent.Name)
		}
		if agent.Login != kind || agent.Version == "" || agent.Package == "" || agent.CredentialFile == "" || len(agent.LoginCommand) == 0 {
			t.Fatalf("incomplete agent declaration: %#v", agent)
		}
		delete(want, agent.Name)
	}
	if len(want) != 0 {
		t.Fatalf("missing agents: %v", want)
	}
}

func TestRegistryReturnsIndependentCommands(t *testing.T) {
	agent, ok := Find("codex")
	if !ok {
		t.Fatal("codex missing")
	}
	agent.LoginCommand[0] = "changed"
	again, _ := Find("codex")
	if again.LoginCommand[0] != "codex" {
		t.Fatalf("registry command changed: %v", again.LoginCommand)
	}
}
