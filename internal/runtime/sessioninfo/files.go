package sessioninfo

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/maryzam/ai-crew-localdev/internal/platform/paths"
	"github.com/maryzam/ai-crew-localdev/internal/platform/securefile"
)

var idPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

const maxBytes = 16 * 1024

type Info struct {
	SessionID  string `json:"session_id"`
	AgentName  string `json:"agent_name"`
	Repo       string `json:"repo"`
	SocketPath string `json:"socket_path"`
}

func Dir() string {
	return filepath.Join(paths.RuntimeDir(), "sessions")
}

func Save(info Info) error {
	if !idPattern.MatchString(info.SessionID) {
		return fmt.Errorf("invalid session ID")
	}
	dir := Dir()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create sessions directory: %w", err)
	}
	data, err := json.Marshal(info)
	if err != nil {
		return fmt.Errorf("marshal session info: %w", err)
	}
	path := filepath.Join(dir, info.SessionID+".json")
	if err := securefile.WriteOwnerOnly(path, data); err != nil {
		return fmt.Errorf("write session file: %w", err)
	}
	return nil
}

func Load(sessionID string) (*Info, error) {
	if !idPattern.MatchString(sessionID) {
		return nil, fmt.Errorf("invalid session ID")
	}
	path := filepath.Join(Dir(), sessionID+".json")
	data, err := securefile.ReadOwnerOnly(path, maxBytes)
	if err != nil {
		return nil, fmt.Errorf("read session file: %w", err)
	}
	var info Info
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, fmt.Errorf("parse session file: %w", err)
	}
	return &info, nil
}

func Remove(sessionID string) error {
	if !idPattern.MatchString(sessionID) {
		return fmt.Errorf("invalid session ID")
	}
	path := filepath.Join(Dir(), sessionID+".json")
	return securefile.Remove(path)
}

func List() ([]string, error) {
	dir := Dir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var ids []string
	for _, entry := range entries {
		name := entry.Name()
		if filepath.Ext(name) == ".json" {
			id := name[:len(name)-5]
			if idPattern.MatchString(id) && entry.Type().IsRegular() {
				ids = append(ids, id)
			}
		}
	}
	return ids, nil
}
