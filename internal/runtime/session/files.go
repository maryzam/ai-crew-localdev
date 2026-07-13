package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/maryzam/ai-crew-localdev/internal/platform/paths"
	"github.com/maryzam/ai-crew-localdev/internal/platform/securefile"
)

var infoIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

const maxInfoBytes = 16 * 1024

type Info struct {
	SessionID  string `json:"session_id"`
	AgentName  string `json:"agent_name"`
	Repo       string `json:"repo"`
	SocketPath string `json:"socket_path"`
}

func InfoDir() string {
	return filepath.Join(paths.RuntimeDir(), "sessions")
}

func SaveInfo(info Info) error {
	if !infoIDPattern.MatchString(info.SessionID) {
		return fmt.Errorf("invalid session ID")
	}
	dir := InfoDir()
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

func LoadInfo(sessionID string) (*Info, error) {
	if !infoIDPattern.MatchString(sessionID) {
		return nil, fmt.Errorf("invalid session ID")
	}
	path := filepath.Join(InfoDir(), sessionID+".json")
	data, err := securefile.ReadOwnerOnly(path, maxInfoBytes)
	if err != nil {
		return nil, fmt.Errorf("read session file: %w", err)
	}
	var info Info
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, fmt.Errorf("parse session file: %w", err)
	}
	return &info, nil
}

func RemoveInfo(sessionID string) error {
	if !infoIDPattern.MatchString(sessionID) {
		return fmt.Errorf("invalid session ID")
	}
	path := filepath.Join(InfoDir(), sessionID+".json")
	return securefile.Remove(path)
}

func ListInfo() ([]string, error) {
	dir := InfoDir()
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
			if infoIDPattern.MatchString(id) && entry.Type().IsRegular() {
				ids = append(ids, id)
			}
		}
	}
	return ids, nil
}
