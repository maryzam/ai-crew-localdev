package agents

import "testing"

func TestDefaultRegistryDeclaresEveryLoginContract(t *testing.T) {
	want := map[string]LoginKind{"claude": Claude, "codex": Codex}
	for _, agent := range Default().All() {
		kind, ok := want[agent.Name]
		if !ok {
			t.Fatalf("unexpected agent %q", agent.Name)
		}
		if agent.Login != kind || agent.Version == "" || agent.Package == "" || agent.CredentialFile == "" || agent.CredentialLimit <= 0 || len(agent.LoginCommand) == 0 {
			t.Fatalf("incomplete agent declaration: %#v", agent)
		}
		if agent.Login == Codex && (agent.Refresh == nil || agent.Refresh.SHA256["amd64"] == "" || agent.Refresh.SHA256["arm64"] == "") {
			t.Fatalf("codex refresh binary is not pinned: %#v", agent.Refresh)
		}
		delete(want, agent.Name)
	}
	if len(want) != 0 {
		t.Fatalf("missing agents: %v", want)
	}
}
