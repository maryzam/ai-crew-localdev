package modelproxy

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testSecret = "sk-ant-oat01-test-only-not-a-real-credential_0123456789"

func writeTokenFile(t *testing.T, content string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "claude-token")
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadClaudeSetupTokenJoinsTerminalWrappedToken(t *testing.T) {
	wrapped := testSecret[:30] + "\n" + testSecret[30:] + "\n"
	cred, err := LoadClaudeSetupToken(writeTokenFile(t, wrapped, 0o600))
	if err != nil {
		t.Fatal(err)
	}
	if cred.secret != testSecret {
		t.Fatalf("loaded credential has length %d, want %d", cred.Len(), len(testSecret))
	}
}

func TestLoadClaudeSetupTokenRejectsUnsafeOrMalformedFiles(t *testing.T) {
	cases := map[string]func(t *testing.T) string{
		"group readable": func(t *testing.T) string { return writeTokenFile(t, testSecret, 0o640) },
		"world readable": func(t *testing.T) string { return writeTokenFile(t, testSecret, 0o604) },
		"symlink": func(t *testing.T) string {
			target := writeTokenFile(t, testSecret, 0o600)
			link := filepath.Join(t.TempDir(), "link")
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}
			return link
		},
		"api key":    func(t *testing.T) string { return writeTokenFile(t, "sk-ant-api03-abcdef", 0o600) },
		"two tokens": func(t *testing.T) string { return writeTokenFile(t, testSecret+"\n"+testSecret+"\n", 0o600) },
		"empty":      func(t *testing.T) string { return writeTokenFile(t, "\n", 0o600) },
	}
	for name, setup := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := LoadClaudeSetupToken(setup(t)); err == nil {
				t.Fatal("expected the credential file to be rejected")
			}
		})
	}
}

func TestCredentialNeverFormatsItsSecret(t *testing.T) {
	cred := Credential{secret: testSecret}
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q"} {
		for _, value := range []any{cred, &cred, Config{Credential: cred}} {
			if out := fmt.Sprintf(verb, value); strings.Contains(out, "test-only") {
				t.Fatalf("%s of %T exposes the credential: %s", verb, value, out)
			}
		}
	}
}
