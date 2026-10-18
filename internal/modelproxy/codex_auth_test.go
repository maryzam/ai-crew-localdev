package modelproxy

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testCodexJWT(expires time.Time) string {
	claims, _ := json.Marshal(map[string]int64{"exp": expires.Unix()})
	return "test." + base64.RawURLEncoding.EncodeToString(claims) + ".signature"
}

func writeCodexAuth(t *testing.T, path, access string, mode os.FileMode) {
	t.Helper()
	data, _ := json.Marshal(map[string]any{
		"auth_mode": "chatgpt",
		"tokens": map[string]string{
			"access_token":  access,
			"refresh_token": "TEST_REFRESH_ONLY",
			"account_id":    "acct_test",
		},
	})
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func TestCodexAuthRejectsUnsafeOrIncompleteCache(t *testing.T) {
	good := testCodexJWT(time.Now().Add(time.Hour))
	for name, setup := range map[string]func(*testing.T) string{
		"group readable": func(t *testing.T) string {
			p := filepath.Join(t.TempDir(), "auth.json")
			writeCodexAuth(t, p, good, 0o640)
			return p
		},
		"symlink": func(t *testing.T) string {
			p := filepath.Join(t.TempDir(), "auth.json")
			writeCodexAuth(t, p, good, 0o600)
			link := filepath.Join(t.TempDir(), "link")
			if err := os.Symlink(p, link); err != nil {
				t.Fatal(err)
			}
			return link
		},
		"malformed token": func(t *testing.T) string {
			p := filepath.Join(t.TempDir(), "auth.json")
			writeCodexAuth(t, p, "not-a-jwt", 0o600)
			return p
		},
		"api key login": func(t *testing.T) string {
			p := filepath.Join(t.TempDir(), "auth.json")
			if err := os.WriteFile(p, []byte(`{"auth_mode":"apikey","OPENAI_API_KEY":"test"}`), 0o600); err != nil {
				t.Fatal(err)
			}
			return p
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := readCodexAuth(setup(t)); err == nil {
				t.Fatal("unsafe or incomplete Codex cache was accepted")
			}
		})
	}
}

func TestCodexAuthRefreshesOnExpiryAndUnauthorized(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	oldAccess := testCodexJWT(time.Now().Add(time.Minute))
	newAccess := testCodexJWT(time.Now().Add(time.Hour))
	writeCodexAuth(t, path, oldAccess, 0o600)
	recorder := newRecorder()
	source, err := NewCodexAuthSource(path, "/bin/true", recorder)
	if err != nil {
		t.Fatal(err)
	}
	refreshes := 0
	source.refresh = func(context.Context) error {
		refreshes++
		writeCodexAuth(t, path, newAccess, 0o600)
		return nil
	}
	credential, err := source.Resolve(context.Background())
	if err != nil || credential.accessToken != newAccess || refreshes != 1 {
		t.Fatalf("refresh result: %v, count %d", err, refreshes)
	}
	if events := recorder.waitAttempts(t, 1); len(events) != 1 || events[0].Decision != RefreshRequested {
		t.Fatalf("refresh evidence %+v", events)
	}
	if _, err := source.Resolve(context.Background()); err != nil || refreshes != 1 {
		t.Fatalf("unexpected cached refresh: %v, count %d", err, refreshes)
	}
	source.Invalidate()
	if _, err := source.Resolve(context.Background()); err != nil || refreshes != 2 {
		t.Fatalf("unauthorized response did not force refresh: %v, count %d", err, refreshes)
	}
}

func TestCodexAuthFailsClosedBeforeUnauditedRefresh(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	writeCodexAuth(t, path, testCodexJWT(time.Now().Add(time.Minute)), 0o600)
	source, err := NewCodexAuthSource(path, "/bin/true", failingAfter(0))
	if err != nil {
		t.Fatal(err)
	}
	called := false
	source.refresh = func(context.Context) error { called = true; return nil }
	if _, err := source.Resolve(context.Background()); err == nil || called {
		t.Fatalf("refresh proceeded without evidence: err=%v called=%v", err, called)
	}
}

func TestCodexNativeRefreshUsesDocumentedAppServerProtocol(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	writeCodexAuth(t, path, testCodexJWT(time.Now().Add(time.Hour)), 0o600)
	bin := filepath.Join(dir, "fake-codex")
	script := "#!/bin/sh\nread init\nprintf '%s\\n' '{\"id\":1,\"result\":{}}'\nread initialized\nread refresh\ncase \"$refresh\" in *account/read*refreshToken*) printf '%s\\n' '{\"id\":2,\"result\":{\"account\":{\"type\":\"chatgpt\"}}}' ;; *) exit 1 ;; esac\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	source, err := NewCodexAuthSource(path, bin, newRecorder())
	if err != nil {
		t.Fatal(err)
	}
	if err := source.refreshWithNativeCLI(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestCodexCredentialFormattingRedactsAccessToken(t *testing.T) {
	credential := CodexCredential{accessToken: "TEST_ACCESS_SECRET", accountID: "acct_test"}
	for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q"} {
		if out := fmt.Sprintf(verb, credential); strings.Contains(out, "TEST_ACCESS_SECRET") {
			t.Fatalf("%s exposed access token", verb)
		}
	}
}
