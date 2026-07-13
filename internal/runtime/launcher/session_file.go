package launcher

import sessionstore "github.com/maryzam/ai-crew-localdev/internal/runtime/session"

type SessionInfo = sessionstore.Info

func sessionsDir() string {
	return sessionstore.InfoDir()
}

func SaveSessionInfo(info SessionInfo) error {
	return sessionstore.SaveInfo(info)
}

func LoadSessionInfo(sessionID string) (*SessionInfo, error) {
	return sessionstore.LoadInfo(sessionID)
}

func RemoveSessionInfo(sessionID string) error {
	return sessionstore.RemoveInfo(sessionID)
}

func ListSessionFiles() ([]string, error) {
	return sessionstore.ListInfo()
}
