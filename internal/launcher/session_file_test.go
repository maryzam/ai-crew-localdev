package launcher

import (
	"os"
	"testing"
)

func TestSessionFileRoundTrip(t *testing.T) {
	// Override runtime dir for test isolation.
	dir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", dir)

	info := SessionInfo{
		SessionID:  "test-sess-001",
		BindSecret: []byte("secret-bytes"),
		AgentName:  "claude",
		Repo:       "owner/repo",
		SocketPath: "/run/user/1000/ai-agent/broker.sock",
	}

	if err := SaveSessionInfo(info); err != nil {
		t.Fatalf("SaveSessionInfo: %v", err)
	}

	got, err := LoadSessionInfo("test-sess-001")
	if err != nil {
		t.Fatalf("LoadSessionInfo: %v", err)
	}

	if got.SessionID != info.SessionID {
		t.Errorf("SessionID = %q, want %q", got.SessionID, info.SessionID)
	}
	if got.AgentName != info.AgentName {
		t.Errorf("AgentName = %q, want %q", got.AgentName, info.AgentName)
	}
	if got.Repo != info.Repo {
		t.Errorf("Repo = %q, want %q", got.Repo, info.Repo)
	}
}

func TestSessionFileList(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", dir)

	// Save two sessions.
	for _, id := range []string{"sess-a", "sess-b"} {
		if err := SaveSessionInfo(SessionInfo{
			SessionID: id,
			AgentName: "test",
			Repo:      "o/r",
		}); err != nil {
			t.Fatal(err)
		}
	}

	ids, err := ListSessionFiles()
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 {
		t.Errorf("got %d sessions, want 2", len(ids))
	}
}

func TestSessionFileRemove(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", dir)

	info := SessionInfo{SessionID: "remove-me", AgentName: "test", Repo: "o/r"}
	SaveSessionInfo(info)

	if err := RemoveSessionInfo("remove-me"); err != nil {
		t.Fatal(err)
	}

	_, err := LoadSessionInfo("remove-me")
	if err == nil {
		t.Error("expected error after removal")
	}
}

func TestLoadSessionFileNotFound(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", dir)

	_, err := LoadSessionInfo("nonexistent")
	if err == nil {
		t.Error("expected error for nonexistent session")
	}
}

func TestListSessionFilesEmptyDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", dir)

	ids, err := ListSessionFiles()
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 0 {
		t.Errorf("expected empty list, got %d", len(ids))
	}

	// Clean up.
	os.RemoveAll(dir)
}
