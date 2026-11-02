package login

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/product/securefile"
)

const claudeTokenPrefix = "sk-ant-oat01-"

var terminalControl = regexp.MustCompile(`\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b[@-Z\\-_]`)
var claudeTokenShape = regexp.MustCompile(`^` + claudeTokenPrefix + `[A-Za-z0-9_-]{32,}$`)

func claudeCredential(output []byte) ([]byte, error) {
	text := strings.ReplaceAll(terminalControl.ReplaceAllString(string(output), ""), "\r\n", "\n")
	lines := strings.Split(strings.ReplaceAll(text, "\r", "\n"), "\n")
	distinct := map[string]struct{}{}
	for index := range lines {
		for _, token := range claudeTokensAt(lines, index) {
			distinct[token] = struct{}{}
		}
	}
	if len(distinct) != 1 {
		return nil, errors.New("claude login did not produce exactly one setup-token")
	}
	var token string
	for token = range distinct {
	}
	if !claudeTokenShape.MatchString(token) {
		return nil, errors.New("claude login produced a malformed setup-token")
	}
	return []byte(token), nil
}

func claudeTokensAt(lines []string, index int) []string {
	var tokens []string
	rest := lines[index]
	for {
		start := strings.Index(rest, claudeTokenPrefix)
		if start < 0 {
			return tokens
		}
		token := tokenRun(rest[start:])
		rest = rest[start+len(token):]
		if strings.TrimSpace(rest) == "" {
			token += wrappedContinuation(lines[index+1:])
		}
		tokens = append(tokens, token)
	}
}

func wrappedContinuation(lines []string) string {
	var continuation strings.Builder
	for _, line := range lines {
		fragment := strings.TrimSpace(line)
		if fragment == "" || tokenRun(fragment) != fragment || strings.Contains(fragment, claudeTokenPrefix) {
			break
		}
		continuation.WriteString(fragment)
	}
	return continuation.String()
}

func tokenRun(text string) string {
	end := strings.IndexFunc(text, func(r rune) bool { return !isTokenRune(r) })
	if end < 0 {
		return text
	}
	return text[:end]
}

func isTokenRune(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_'
}

func codexCredential(path string, limit int64, now time.Time) ([]byte, error) {
	data, err := securefile.ReadOwnerOnly(path, limit)
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
