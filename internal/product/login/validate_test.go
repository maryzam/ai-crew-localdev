package login

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const validClaudeToken = "sk-ant-oat01-abcdefghijklmnopqrstuvwxyz_0123456789-ABCDEFG"

func TestClaudeCredentialExtractsOneNativeSetupToken(t *testing.T) {
	output := []byte("Open the browser\r\n\x1b[32m" + validClaudeToken + "\x1b[0m\r\nToken created.\r\n")
	credential, err := claudeCredential(output)
	if err != nil {
		t.Fatal(err)
	}
	if string(credential) != validClaudeToken+"\n" {
		t.Fatalf("credential = %q", credential)
	}
	for _, invalid := range [][]byte{[]byte("no token"), []byte(validClaudeToken + "\n" + validClaudeToken), []byte("sk-ant-api03-not-a-setup-token")} {
		if _, err := claudeCredential(invalid); err == nil {
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
	credential, err := codexCredential(path, time.Unix(1_700_000_000, 0))
	if err != nil || string(credential) != valid {
		t.Fatalf("credential = %q, error = %v", credential, err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := codexCredential(path, time.Unix(1_700_000_000, 0)); err == nil || !strings.Contains(err.Error(), "owner-only") {
		t.Fatalf("unsafe cache error = %v", err)
	}
}
