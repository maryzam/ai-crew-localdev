package launcher

import sessioninfo "github.com/maryzam/ai-crew-localdev/internal/runtime/sessioninfo"

type SessionInfo = sessioninfo.Info

func SaveSessionInfo(info SessionInfo) error {
	return sessioninfo.Save(info)
}

func LoadSessionInfo(sessionID string) (*SessionInfo, error) {
	return sessioninfo.Load(sessionID)
}

func RemoveSessionInfo(sessionID string) error {
	return sessioninfo.Remove(sessionID)
}
