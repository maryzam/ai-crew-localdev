package sessioninfo

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestFileContainsOnlySessionMetadata(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	info := Info{
		SessionID:  "sess-x",
		AgentName:  "claude",
		Repo:       "o/r",
		SocketPath: "/run/user/1000/ai-agent/broker.sock",
	}
	if err := Save(info); err != nil {
		t.Fatalf("Save: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(Dir(), "sess-x.json"))
	if err != nil {
		t.Fatalf("read session file: %v", err)
	}
	var got map[string]string
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("parse session file: %v", err)
	}
	want := map[string]string{
		"session_id":  info.SessionID,
		"agent_name":  info.AgentName,
		"repo":        info.Repo,
		"socket_path": info.SocketPath,
	}
	if len(got) != len(want) {
		t.Fatalf("session file keys = %v, want %v", got, want)
	}
	for key, wantValue := range want {
		if got[key] != wantValue {
			t.Fatalf("session file %s = %q, want %q", key, got[key], wantValue)
		}
	}
}

func TestRoundTrip(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	info := Info{
		SessionID:  "test-sess-001",
		AgentName:  "claude",
		Repo:       "owner/repo",
		SocketPath: "/run/user/1000/ai-agent/broker.sock",
	}

	if err := Save(info); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := Load("test-sess-001")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.SessionID != info.SessionID || got.AgentName != info.AgentName || got.Repo != info.Repo || got.SocketPath != info.SocketPath {
		t.Fatalf("loaded info = %#v, want %#v", got, info)
	}
}

func TestList(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	for _, id := range []string{"sess-a", "sess-b"} {
		if err := Save(Info{SessionID: id, AgentName: "test", Repo: "o/r"}); err != nil {
			t.Fatal(err)
		}
	}

	ids, err := List()
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(ids)
	if !slices.Equal(ids, []string{"sess-a", "sess-b"}) {
		t.Errorf("got sessions %v, want [sess-a sess-b]", ids)
	}
}

func TestRemove(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	if err := Save(Info{SessionID: "remove-me", AgentName: "test", Repo: "o/r"}); err != nil {
		t.Fatal(err)
	}

	if err := Remove("remove-me"); err != nil {
		t.Fatal(err)
	}

	_, err := Load("remove-me")
	if err == nil {
		t.Fatal("expected error after removal")
	}
}

func TestLoadNotFound(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	_, err := Load("nonexistent")
	if err == nil {
		t.Fatal("expected error for nonexistent session")
	}
}

func TestRejectsPathTraversal(t *testing.T) {
	for _, sessionID := range []string{"../outside", "nested/file", ".", ""} {
		if _, err := Load(sessionID); err == nil {
			t.Errorf("Load(%q) succeeded", sessionID)
		}
		if err := Remove(sessionID); err == nil {
			t.Errorf("Remove(%q) succeeded", sessionID)
		}
	}
}

func TestListEmptyDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", dir)

	ids, err := List()
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 0 {
		t.Errorf("expected empty list, got %d", len(ids))
	}

	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	ids, err = List()
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 0 {
		t.Errorf("expected empty missing-dir list, got %d", len(ids))
	}
}
