package modelproxy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "app-server" {
		os.Exit(runRefreshFixture())
	}
	if len(os.Args) > 1 && os.Args[1] == "fixture-stdout-child" {
		time.Sleep(time.Second)
		_ = os.WriteFile(filepath.Join(os.Getenv("CODEX_HOME"), "fixture-child-survived"), nil, 0o600)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func runRefreshFixture() int {
	home := os.Getenv("CODEX_HOME")
	if home == "" || home != os.Getenv("HOME") {
		return 2
	}
	config, err := os.ReadFile(filepath.Join(home, "fixture.json"))
	if err != nil {
		return 2
	}
	var fixture struct {
		Mode    string `json:"mode"`
		Access  string `json:"access"`
		Refresh string `json:"refresh"`
	}
	if json.Unmarshal(config, &fixture) != nil {
		return 2
	}
	count, err := os.OpenFile(filepath.Join(home, "fixture-count"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return 2
	}
	_, _ = count.WriteString("attempt\n")
	_ = count.Close()
	decoder := json.NewDecoder(os.Stdin)
	var message struct {
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if decoder.Decode(&message) != nil || message.Method != "initialize" {
		return 3
	}
	if _, err := fmt.Fprintln(os.Stdout, `{"id":1,"result":{}}`); err != nil {
		return 3
	}
	if decoder.Decode(&message) != nil || message.Method != "initialized" {
		return 3
	}
	if decoder.Decode(&message) != nil || message.Method != "account/read" || !strings.Contains(string(message.Params), `"refreshToken":true`) {
		return 3
	}
	if fixture.Mode == "hang" {
		time.Sleep(10 * time.Second)
		return 4
	}
	if fixture.Mode == "held" {
		deadline := time.Now().Add(5 * time.Second)
		for {
			if _, err := os.Stat(filepath.Join(home, "fixture-release")); err == nil {
				break
			}
			if time.Now().After(deadline) {
				return 4
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	if _, err := fmt.Fprintln(os.Stdout, `{"id":2,"result":{"account":{"type":"chatgpt"}}}`); err != nil {
		return 3
	}
	if fixture.Mode != "linger" {
		time.Sleep(150 * time.Millisecond)
	}
	if fixture.Mode == "partial" {
		fixture.Refresh = "TEST_REFRESH_ONLY"
	}
	data, _ := json.Marshal(map[string]any{"auth_mode": "chatgpt", "tokens": map[string]string{"access_token": fixture.Access, "refresh_token": fixture.Refresh, "account_id": "acct_test"}})
	temp := filepath.Join(home, "auth.next")
	if os.WriteFile(temp, data, 0o600) != nil || os.Rename(temp, filepath.Join(home, "auth.json")) != nil {
		return 4
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
	if fixture.Mode == "linger" {
		time.Sleep(10 * time.Second)
	}
	if fixture.Mode == "child-stdout" {
		child := exec.Command(os.Args[0], "fixture-stdout-child")
		child.Stdout = os.Stdout
		child.Stderr = io.Discard
		if err := child.Start(); err != nil {
			return 4
		}
		_ = child.Process.Release()
	}
	return 0
}

func testRefreshBinary(t *testing.T) (string, string) {
	t.Helper()
	source, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "codex")
	if err := os.WriteFile(path, data, 0o700); err != nil {
		t.Fatal(err)
	}
	return path, pinnedTestBinary(t, path)
}

func testCodexSource(t *testing.T, path string, recorder Recorder) (*CodexAuthSource, string) {
	t.Helper()
	binary, hash := testRefreshBinary(t)
	source, err := NewCodexAuthSource(path, binary, hash, recorder)
	if err != nil {
		t.Fatal(err)
	}
	return source, binary
}

func setRefreshFixture(t *testing.T, authPath, mode, access string) {
	t.Helper()
	data, _ := json.Marshal(map[string]string{"mode": mode, "access": access, "refresh": "TEST_REFRESH_ROTATED"})
	if err := os.WriteFile(filepath.Join(filepath.Dir(authPath), "fixture.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func fixtureAttempts(t *testing.T, authPath string) int {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(filepath.Dir(authPath), "fixture-count"))
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(data), "attempt\n")
}

func TestCodexRefreshSurvivesDisconnectAndIgnoresStale401(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	oldAccess := testCodexJWT(time.Now().Add(time.Minute))
	newAccess := testCodexJWT(time.Now().Add(time.Hour))
	writeCodexAuth(t, path, oldAccess, 0o600)
	setRefreshFixture(t, path, "held", newAccess)
	recorder := newRecorder()
	source, _ := testCodexSource(t, path, recorder)
	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() { _, err := source.Resolve(ctx); first <- err }()
	deadline := time.Now().Add(3 * time.Second)
	for fixtureAttempts(t, path) == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if fixtureAttempts(t, path) != 1 {
		t.Fatal("native refresh did not start")
	}
	cancel()
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatalf("disconnected caller returned %v", err)
	}
	pausedAuth := path + ".paused"
	if err := os.Rename(path, pausedAuth); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errorsSeen := make(chan error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			credential, err := source.Resolve(context.Background())
			if err == nil && credential.accessToken != newAccess {
				err = errors.New("shared refresh returned the wrong token")
			}
			if err != nil {
				errorsSeen <- err
			}
		}()
	}
	time.Sleep(50 * time.Millisecond)
	if err := os.Rename(pausedAuth, path); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-errorsSeen:
		t.Fatalf("concurrent request read auth during refresh: %v", err)
	default:
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), "fixture-release"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	close(errorsSeen)
	for err := range errorsSeen {
		t.Fatal(err)
	}
	source.Invalidate(oldAccess)
	credential, err := source.Resolve(context.Background())
	if err != nil || credential.accessToken != newAccess || fixtureAttempts(t, path) != 1 {
		t.Fatalf("stale 401 retriggered refresh: %v", err)
	}
	events := recorder.waitAttempts(t, 2)
	if events[0].Decision != RefreshRequested || events[1].Decision != RefreshRotated {
		t.Fatalf("refresh evidence: %+v", events)
	}
}

func TestCodexProxyRecoversAfter401(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	oldAccess := testCodexJWT(time.Now().Add(time.Hour))
	newAccess := testCodexJWT(time.Now().Add(2 * time.Hour))
	writeCodexAuth(t, path, oldAccess, 0o600)
	setRefreshFixture(t, path, "success", newAccess)
	recorder := newRecorder()
	source, _ := testCodexSource(t, path, recorder)
	provider := newFakeProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer "+oldAccess {
			w.Header().Set("Www-Authenticate", `Bearer error="invalid_token"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+newAccess {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	proxy := newTestCodexProxy(t, provider, source, recorder)
	first := modelRequest(t, proxy.URL, http.MethodPost, codexPrefix+"/responses", "{}", codexHeaders())
	if first.StatusCode != http.StatusUnauthorized {
		t.Fatalf("first provider response was %d", first.StatusCode)
	}
	second := modelRequest(t, proxy.URL, http.MethodPost, codexPrefix+"/responses", "{}", codexHeaders())
	if second.StatusCode != http.StatusOK || fixtureAttempts(t, path) != 1 {
		t.Fatalf("Codex did not recover after 401: status %d", second.StatusCode)
	}
}

func TestCodexOpaque401DoesNotSpendRefreshBudget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	writeCodexAuth(t, path, testCodexJWT(time.Now().Add(time.Hour)), 0o600)
	setRefreshFixture(t, path, "success", testCodexJWT(time.Now().Add(2*time.Hour)))
	recorder := newRecorder()
	source, _ := testCodexSource(t, path, recorder)
	provider := newFakeProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `{"error":{"type":"invalid_request_error"}}`)
	})
	proxy := newTestCodexProxy(t, provider, source, recorder)
	for i := 0; i < 2; i++ {
		response := modelRequest(t, proxy.URL, http.MethodPost, codexPrefix+"/responses", "{}", codexHeaders())
		if response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("unclassified 401 changed to %d", response.StatusCode)
		}
	}
	if fixtureAttempts(t, path) != 0 || provider.callCount() != 2 {
		t.Fatal("unclassified 401 consumed the refresh budget")
	}
}

func TestCodexRefreshRejectsIncompleteRotation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	writeCodexAuth(t, path, testCodexJWT(time.Now().Add(time.Minute)), 0o600)
	setRefreshFixture(t, path, "partial", testCodexJWT(time.Now().Add(time.Hour)))
	recorder := newRecorder()
	source, _ := testCodexSource(t, path, recorder)
	if _, err := source.Resolve(context.Background()); err == nil {
		t.Fatal("partial token rotation was accepted")
	}
	if _, err := source.Resolve(context.Background()); err == nil || fixtureAttempts(t, path) != 1 {
		t.Fatal("incomplete broker login was reused")
	}
	events := recorder.waitAttempts(t, 2)
	if events[0].Decision != RefreshRequested || events[1].Decision != RefreshFailed {
		t.Fatalf("incomplete rotation evidence: %+v", events)
	}
}

func TestCodexRotatedLoginNeedsDurableOutcomeEvidence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	writeCodexAuth(t, path, testCodexJWT(time.Now().Add(time.Minute)), 0o600)
	setRefreshFixture(t, path, "success", testCodexJWT(time.Now().Add(time.Hour)))
	source, _ := testCodexSource(t, path, failingAfter(1))
	if _, err := source.Resolve(context.Background()); err == nil {
		t.Fatal("rotated credential was released without outcome evidence")
	}
	if _, err := source.Resolve(context.Background()); err == nil || fixtureAttempts(t, path) != 1 {
		t.Fatal("failed outcome evidence did not latch the source closed")
	}
}

func TestCodexRefreshHonorsHostDeadline(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	writeCodexAuth(t, path, testCodexJWT(time.Now().Add(5*time.Second)), 0o600)
	setRefreshFixture(t, path, "hang", testCodexJWT(time.Now().Add(time.Hour)))
	source, _ := testCodexSource(t, path, newRecorder())
	source.refreshTimeout = 150 * time.Millisecond
	start := time.Now()
	if _, err := source.Resolve(context.Background()); err == nil || time.Since(start) > 3*time.Second {
		t.Fatal("native refresh exceeded its host deadline")
	}
}

func TestCodexCompleteRotationSurvivesNativeProcessTimeout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	oldAccess := testCodexJWT(time.Now().Add(time.Minute))
	newAccess := testCodexJWT(time.Now().Add(time.Hour))
	writeCodexAuth(t, path, oldAccess, 0o600)
	setRefreshFixture(t, path, "linger", newAccess)
	recorder := newRecorder()
	source, _ := testCodexSource(t, path, recorder)
	source.refreshTimeout = 350 * time.Millisecond
	provider := newFakeProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+newAccess {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	proxy := newTestCodexProxy(t, provider, source, recorder)
	response := modelRequest(t, proxy.URL, http.MethodPost, codexPrefix+"/responses", "{}", codexHeaders())
	if response.StatusCode != http.StatusOK || fixtureAttempts(t, path) != 1 {
		t.Fatalf("complete rotation was discarded after process timeout: status %d", response.StatusCode)
	}
	events := recorder.waitAttempts(t, 4)
	if events[1].Decision != RefreshRotated {
		t.Fatalf("rotation outcome %+v", decisions(events))
	}
}

func TestCodexRefreshBoundsInheritedStdoutAndReleasesParallelCallers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	oldAccess := testCodexJWT(time.Now().Add(time.Minute))
	newAccess := testCodexJWT(time.Now().Add(time.Hour))
	writeCodexAuth(t, path, oldAccess, 0o600)
	setRefreshFixture(t, path, "child-stdout", newAccess)
	recorder := newRecorder()
	source, _ := testCodexSource(t, path, recorder)
	source.refreshTimeout = 300 * time.Millisecond
	provider := newFakeProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+newAccess {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	proxy := newTestCodexProxy(t, provider, source, recorder)
	start := time.Now()
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			request, err := http.NewRequestWithContext(ctx, http.MethodPost, proxy.URL+codexPrefix+"/responses", strings.NewReader("{}"))
			if err == nil {
				request.Header.Set("Authorization", "Bearer "+codexPlaceholder)
				var response *http.Response
				response, err = http.DefaultClient.Do(request)
				if err == nil {
					_ = response.Body.Close()
					if response.StatusCode != http.StatusOK {
						err = fmt.Errorf("proxy returned %d", response.StatusCode)
					}
				}
			}
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatalf("parallel request failed: %v", err)
		}
	}
	if time.Since(start) > 800*time.Millisecond || fixtureAttempts(t, path) != 1 {
		t.Fatal("inherited stdout delayed or duplicated native refresh")
	}
	events := recorder.waitAttempts(t, 6)
	if events[1].Decision != RefreshRotated || events[1].Reason == "" || events[1].DurationMS == 0 {
		t.Fatalf("refresh outcome %+v", decisions(events))
	}
	time.Sleep(1200 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), "fixture-child-survived")); !os.IsNotExist(err) {
		t.Fatal("refresh child survived the process-group deadline")
	}
}

func TestCodexProactiveRefreshFailureKeepsValidLogin(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	access := testCodexJWT(time.Now().Add(4 * time.Minute))
	writeCodexAuth(t, path, access, 0o600)
	setRefreshFixture(t, path, "hang", testCodexJWT(time.Now().Add(time.Hour)))
	recorder := newRecorder()
	source, _ := testCodexSource(t, path, recorder)
	source.refreshTimeout = 150 * time.Millisecond
	provider := newFakeProvider(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+access {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	proxy := newTestCodexProxy(t, provider, source, recorder)
	for i := 0; i < 2; i++ {
		response := modelRequest(t, proxy.URL, http.MethodPost, codexPrefix+"/responses", "{}", codexHeaders())
		if response.StatusCode != http.StatusOK {
			t.Fatalf("valid login failed during refresh backoff: status %d", response.StatusCode)
		}
	}
	if fixtureAttempts(t, path) != 1 {
		t.Fatal("refresh backoff started another native process")
	}
	events := recorder.waitAttempts(t, 8)
	if events[1].Decision != RefreshFailed || events[2].Decision != RefreshDeferred {
		t.Fatalf("refresh failure evidence %+v", decisions(events))
	}
}

func TestCodexPinRejectsLauncherAndChangedBinary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "auth.json")
	access := testCodexJWT(time.Now().Add(time.Hour))
	writeCodexAuth(t, path, access, 0o600)
	binary, hash := testRefreshBinary(t)
	if _, err := NewCodexAuthSource(path, binary, "", newRecorder()); err == nil {
		t.Fatal("unpinned refresh binary was accepted")
	}
	launcher := filepath.Join(filepath.Dir(binary), "launcher")
	if err := os.WriteFile(launcher, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := NewCodexAuthSource(path, launcher, pinnedTestBinary(t, launcher), newRecorder()); err == nil {
		t.Fatal("launcher was accepted as the pinned program")
	}
	operatorHome := t.TempDir()
	t.Setenv("HOME", operatorHome)
	operatorBinaryDir := filepath.Join(operatorHome, ".codex", "packages")
	if err := os.MkdirAll(operatorBinaryDir, 0o700); err != nil {
		t.Fatal(err)
	}
	operatorBinary := filepath.Join(operatorBinaryDir, "codex")
	data, err := os.ReadFile(binary)
	if err != nil || os.WriteFile(operatorBinary, data, 0o700) != nil {
		t.Fatal("cannot stage operator-owned Codex binary")
	}
	if _, err := NewCodexAuthSource(path, operatorBinary, hash, newRecorder()); err == nil {
		t.Fatal("operator's updating Codex home was accepted as a pinned binary source")
	}
	source, err := NewCodexAuthSource(path, binary, hash, newRecorder())
	if err != nil {
		t.Fatal(err)
	}
	setRefreshFixture(t, path, "success", testCodexJWT(time.Now().Add(2*time.Hour)))
	data[len(data)-1] ^= 1
	if err := os.WriteFile(binary, data, 0o700); err != nil {
		t.Fatal(err)
	}
	source.Invalidate(access)
	if _, err := source.Resolve(context.Background()); err == nil || fixtureAttempts(t, path) != 0 {
		t.Fatal("changed Codex binary was executed")
	}
}
