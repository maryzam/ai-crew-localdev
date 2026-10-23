package login

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const validClaudeToken = "sk-ant-oat01-abcdefghijklmnopqrstuvwxyz_0123456789-ABCDEFGHIJKLMNOPQRSTUVWXYZ"

func TestClaudeCredentialExtractsOneNativeSetupToken(t *testing.T) {
	frame := "Your OAuth token:\r\n\r\n\x1b[32m" + validClaudeToken + "\x1b[0m\r\n\r\nStore this token securely.\r\n"
	wrapped := "Your OAuth token:\r\n\r\n  " + validClaudeToken[:30] + "\r\n  " + validClaudeToken[30:60] + "\r\n  " + validClaudeToken[60:] + "\r\n\r\n  Store this token securely.\r\n"
	accepted := map[string]string{
		"plain":   "Open \x1b]8;;https://claude.ai\x07the browser\x1b]8;;\x07\r\n" + frame,
		"wrapped": wrapped,
		"redrawn": frame + "\x1b[2K\x1b[1A\x1b[2K\x1b[1A\x1b[2K\x1b[G" + frame,
	}
	for name, output := range accepted {
		credential, err := claudeCredential([]byte(output))
		if err != nil || string(credential) != validClaudeToken {
			t.Fatalf("%s: credential = %q, error = %v", name, credential, err)
		}
	}
	other := strings.Replace(validClaudeToken, "abc", "xyz", 1)
	for _, invalid := range []string{"no token", validClaudeToken + "\n" + other, "sk-ant-api03-not-a-setup-token", claudeTokenPrefix + "short\n"} {
		if _, err := claudeCredential([]byte(invalid)); err == nil {
			t.Fatalf("accepted invalid output %q", invalid)
		}
	}
}

func TestCodexCredentialRequiresPrivateCompleteUnexpiredChatGPTCache(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	valid := `{"auth_mode":"chatgpt","tokens":{"access_token":"x.eyJleHAiOjQxMDI0NDQ4MDB9.x","refresh_token":"refresh","account_id":"account"}}`
	if err := os.WriteFile(path, []byte(valid), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_700_000_000, 0)
	credential, err := codexCredential(path, 1<<10, now)
	if err != nil || string(credential) != valid {
		t.Fatalf("credential = %q, error = %v", credential, err)
	}
	if _, err := codexCredential(path, 16, now); err == nil {
		t.Fatal("oversized cache accepted")
	}
	if _, err := codexCredential(path, 1<<10, time.Unix(4_102_444_800, 0)); err == nil {
		t.Fatal("expired access token accepted")
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := codexCredential(path, 1<<10, now); err == nil || !strings.Contains(err.Error(), "owner-only") {
		t.Fatalf("unsafe cache error = %v", err)
	}
}
