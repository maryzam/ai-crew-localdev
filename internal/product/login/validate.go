package login

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/platform/securefile"
)

const claudeTokenPrefix = "sk-ant-oat01-"

var ansiSequence = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
var claudeToken = regexp.MustCompile(`sk-ant-oat01-[A-Za-z0-9_-]{32,}`)

func claudeCredential(output []byte) ([]byte, error) {
	if strings.Count(string(output), claudeTokenPrefix) != 1 {
		return nil, errors.New("claude login did not produce exactly one setup-token")
	}
	clean := ansiSequence.ReplaceAllString(string(output), "")
	result := claudeToken.FindString(clean)
	if result == "" {
		return nil, errors.New("claude login produced a malformed setup-token")
	}
	return []byte(result + "\n"), nil
}

func codexCredential(path string, now time.Time) ([]byte, error) {
	data, err := securefile.ReadOwnerOnly(path, 256<<10)
	if err != nil {
		return nil, err
	}
	var cache struct {
		AuthMode string `json:"auth_mode"`
		Tokens   struct {
			Access  string `json:"access_token"`
			Refresh string `json:"refresh_token"`
			Account string `json:"account_id"`
		} `json:"tokens"`
	}
	if json.Unmarshal(data, &cache) != nil || cache.AuthMode != "chatgpt" || cache.Tokens.Access == "" || cache.Tokens.Refresh == "" || cache.Tokens.Account == "" {
		return nil, errors.New("codex login did not produce a complete ChatGPT credential cache")
	}
	parts := strings.Split(cache.Tokens.Access, ".")
	if len(parts) != 3 {
		return nil, errors.New("codex login produced an access token without an expiry")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, errors.New("codex login produced an access token with an invalid expiry")
	}
	var claims struct {
		Expires int64 `json:"exp"`
	}
	if json.Unmarshal(payload, &claims) != nil || !time.Unix(claims.Expires, 0).After(now.Add(time.Minute)) {
		return nil, errors.New("codex login produced an expired access token")
	}
	return data, nil
}
