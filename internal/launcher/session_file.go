package launcher

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/maryzam/ai-crew-localdev/internal/config"
)

// SessionInfo lets "ai-agent session" commands act on a session without the
// inherited FD. It deliberately omits the bind secret: that lives only in the
// sealed memfd, and revoke/status are authorized by UID at the broker.
type SessionInfo struct {
	SessionID  string `json:"session_id"`
	AgentName  string `json:"agent_name"`
	Repo       string `json:"repo"`
	SocketPath string `json:"socket_path"`
}

// sessionsDir returns the directory where session files are stored.
func sessionsDir() string {
	return filepath.Join(config.RuntimeDir(), "sessions")
}

// SaveSessionInfo writes session info to a runtime file so that other CLI
// commands (e.g. revoke) can reference it.
func SaveSessionInfo(info SessionInfo) error {
	dir := sessionsDir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create sessions directory: %w", err)
	}

	data, err := json.Marshal(info)
	if err != nil {
		return fmt.Errorf("marshal session info: %w", err)
	}

	path := filepath.Join(dir, info.SessionID+".json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		return fmt.Errorf("write session file: %w", err)
	}

	return nil
}

// LoadSessionInfo reads session info from the runtime file.
func LoadSessionInfo(sessionID string) (*SessionInfo, error) {
	path := filepath.Join(sessionsDir(), sessionID+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read session file: %w", err)
	}

	var info SessionInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, fmt.Errorf("parse session file: %w", err)
	}
	return &info, nil
}

// RemoveSessionInfo deletes the session file.
func RemoveSessionInfo(sessionID string) error {
	path := filepath.Join(sessionsDir(), sessionID+".json")
	return os.Remove(path)
}

// ListSessionFiles returns all session IDs that have stored files.
func ListSessionFiles() ([]string, error) {
	dir := sessionsDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var ids []string
	for _, e := range entries {
		name := e.Name()
		if filepath.Ext(name) == ".json" {
			ids = append(ids, name[:len(name)-5])
		}
	}
	return ids, nil
}
