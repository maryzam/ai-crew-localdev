package modelproxy

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testCodexJWT(expires time.Time) string {
	claims, _ := json.Marshal(map[string]int64{"exp": expires.Unix()})
	return "test." + base64.RawURLEncoding.EncodeToString(claims) + ".signature"
}

func writeCodexAuth(t *testing.T, path, access string, mode os.FileMode) {
	t.Helper()
	if err := os.Chmod(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
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

func pinnedTestBinary(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(data))
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

func TestCodexAuthFailsClosedBeforeUnauditedRefresh(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	writeCodexAuth(t, path, testCodexJWT(time.Now().Add(time.Minute)), 0o600)
	source, err := NewCodexAuthSource(path, "/bin/true", pinnedTestBinary(t, "/bin/true"), failingAfter(0))
	if err != nil {
		t.Fatal(err)
	}
	called := false
	source.refresh = func(context.Context) error { called = true; return nil }
	if _, err := source.Resolve(context.Background()); err == nil || called {
		t.Fatalf("refresh proceeded without evidence: err=%v called=%v", err, called)
	}
}

func TestCodexAuthRejectsOperatorLogin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", "")
	dir := filepath.Join(home, ".codex")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "auth.json")
	writeCodexAuth(t, path, testCodexJWT(time.Now().Add(time.Hour)), 0o600)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewCodexAuthSource(path, "/bin/true", pinnedTestBinary(t, "/bin/true"), newRecorder()); err == nil {
		t.Fatal("operator login was accepted for broker refresh")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(before) {
		t.Fatal("operator login changed during rejected refresh setup")
	}
}

func TestCodexRefreshBudgetStopsRepeatedProviderFailures(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	writeCodexAuth(t, path, testCodexJWT(time.Now().Add(time.Hour)), 0o600)
	recorder := newRecorder()
	source, err := NewCodexAuthSource(path, "/bin/true", pinnedTestBinary(t, "/bin/true"), recorder)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	source.now = func() time.Time { return now }
	called := 0
	source.refresh = func(context.Context) error {
		called++
		return errors.New("provider unavailable")
	}
	source.Invalidate()
	for i := 0; i < 2; i++ {
		if _, err := source.Resolve(context.Background()); err == nil {
			t.Fatal("failed refresh returned a credential")
		}
	}
	if called != 1 {
		t.Fatalf("rapid refresh made %d provider calls", called)
	}
	events := recorder.waitAttempts(t, 2)
	if events[0].Decision != RefreshRequested || events[1].Decision != Refused {
		t.Fatalf("refresh budget evidence: %+v", events)
	}
	for i := 0; i < 2; i++ {
		now = now.Add(codexRefreshInterval)
		if _, err := source.Resolve(context.Background()); err == nil {
			t.Fatal("failed refresh returned a credential")
		}
	}
	now = now.Add(codexRefreshInterval)
	if _, err := source.Resolve(context.Background()); err == nil || called != 3 {
		t.Fatal("hourly refresh budget did not fail closed")
	}
}

func TestCodexRefreshRejectsChangedBinary(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	writeCodexAuth(t, path, testCodexJWT(time.Now().Add(time.Hour)), 0o600)
	bin := filepath.Join(dir, "codex")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := NewCodexAuthSource(path, bin, "", newRecorder()); err == nil {
		t.Fatal("unpinned Codex binary was accepted")
	}
	source, err := NewCodexAuthSource(path, bin, pinnedTestBinary(t, bin), newRecorder())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\ntouch "+filepath.Join(dir, "executed")+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	source.Invalidate()
	if _, err := source.Resolve(context.Background()); err == nil {
		t.Fatal("changed Codex binary was executed")
	}
	if _, err := os.Stat(filepath.Join(dir, "executed")); !os.IsNotExist(err) {
		t.Fatal("changed binary ran before pin verification")
	}
}

func TestCodexRefreshHonorsDeadline(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	writeCodexAuth(t, path, testCodexJWT(time.Now().Add(time.Hour)), 0o600)
	bin := filepath.Join(dir, "codex")
	homes := filepath.Join(dir, "refresh-home")
	script := "#!/bin/sh\nprintf '%s\\n%s\\n' \"$HOME\" \"$CODEX_HOME\" > \"" + homes + "\"\nsleep 5\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	source, err := NewCodexAuthSource(path, bin, pinnedTestBinary(t, bin), newRecorder())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	source.Invalidate()
	if _, err := source.Resolve(ctx); err == nil || time.Since(start) > 3*time.Second {
		t.Fatal("native refresh did not stop within its deadline")
	}
	actual, err := os.ReadFile(homes)
	if err != nil || string(actual) != dir+"\n"+dir+"\n" {
		t.Fatal("native refresh could access the operator home")
	}
}
