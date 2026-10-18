package modelproxy

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

const testPlaceholder = "sk-ant-oat01-placeholder-run-0001"

type memoryRecorder struct {
	mu        sync.Mutex
	events    []Event
	attempts  int
	durableTo int
	failOnly  int
}

func failingAfter(n int) *memoryRecorder { return &memoryRecorder{durableTo: n} }

func failingOnlyAttempt(n int) *memoryRecorder { return &memoryRecorder{durableTo: -1, failOnly: n} }

func (m *memoryRecorder) Record(ev Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.attempts++
	if (m.durableTo >= 0 && m.attempts > m.durableTo) || m.attempts == m.failOnly {
		return errors.New("disk full")
	}
	m.events = append(m.events, ev)
	return nil
}

func (m *memoryRecorder) waitAttempts(t *testing.T, n int) []Event {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		m.mu.Lock()
		attempts, events := m.attempts, append([]Event(nil), m.events...)
		m.mu.Unlock()
		if attempts >= n {
			return events
		}
		if time.Now().After(deadline) {
			t.Fatalf("recorder saw %d attempts, want %d", attempts, n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func newRecorder() *memoryRecorder { return &memoryRecorder{durableTo: -1} }

func decisions(events []Event) []Decision {
	out := make([]Decision, len(events))
	for i, ev := range events {
		out[i] = ev.Decision
	}
	return out
}

type upstreamCall struct {
	method string
	uri    string
	header http.Header
}

type fakeProvider struct {
	server *httptest.Server
	mu     sync.Mutex
	calls  []upstreamCall
}

func newFakeProvider(t *testing.T, respond http.HandlerFunc) *fakeProvider {
	t.Helper()
	fp := &fakeProvider{}
	fp.server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fp.mu.Lock()
		fp.calls = append(fp.calls, upstreamCall{method: r.Method, uri: r.URL.RequestURI(), header: r.Header.Clone()})
		fp.mu.Unlock()
		_, _ = io.Copy(io.Discard, r.Body)
		respond(w, r)
	}))
	t.Cleanup(fp.server.Close)
	return fp
}

func (fp *fakeProvider) received() []upstreamCall {
	fp.mu.Lock()
	defer fp.mu.Unlock()
	return append([]upstreamCall(nil), fp.calls...)
}

func (fp *fakeProvider) callCount() int { return len(fp.received()) }

func newTestProxy(t *testing.T, fp *fakeProvider, rec Recorder) *httptest.Server {
	t.Helper()
	upstream, err := url.Parse(fp.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	p, err := New(Config{
		Upstream:    upstream,
		Placeholder: testPlaceholder,
		Credential:  Credential{secret: testSecret},
		Recorder:    rec,
		Transport:   fp.server.Client().Transport,
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(p)
	t.Cleanup(srv.Close)
	return srv
}

func modelRequest(t *testing.T, base, method, path, body string, header map[string]string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, base+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	return resp
}

func claudeHeaders() map[string]string {
	return map[string]string{
		"Authorization":            "Bearer " + testPlaceholder,
		"Anthropic-Beta":           "oauth-2025-04-20,interleaved-thinking-2025-05-14",
		"Anthropic-Version":        "2023-06-01",
		"X-Claude-Code-Session-Id": "session-1",
		"User-Agent":               "claude-cli/2.1.302 (external, cli)",
	}
}

func okResponse(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, `{"type":"message","content":[{"type":"text","text":"RESPONSE-CONTENT-MARKER"}]}`)
}

func TestForwardsModelRequestWithHostCredentialOnly(t *testing.T) {
	fp := newFakeProvider(t, okResponse)
	rec := newRecorder()
	proxy := newTestProxy(t, fp, rec)

	header := claudeHeaders()
	header["X-Api-Key"] = testPlaceholder
	resp := modelRequest(t, proxy.URL, http.MethodPost, "/v1/messages?beta=true", `{"messages":[{"role":"user","content":"PROMPT-CONTENT-MARKER"}]}`, header)
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "RESPONSE-CONTENT-MARKER") {
		t.Fatalf("status %d body %q", resp.StatusCode, body)
	}

	if fp.callCount() != 1 {
		t.Fatalf("provider saw %d calls, want 1", fp.callCount())
	}
	call := fp.received()[0]
	if call.method != http.MethodPost || call.uri != "/v1/messages?beta=true" {
		t.Fatalf("provider saw %s %s", call.method, call.uri)
	}
	if got := call.header.Get("Authorization"); got != "Bearer "+testSecret {
		t.Fatalf("provider saw authorization %q", got)
	}
	if got := call.header.Get("X-Api-Key"); got != "" {
		t.Fatalf("provider saw x-api-key %q", got)
	}
	if got := call.header.Get("Anthropic-Beta"); got != header["Anthropic-Beta"] {
		t.Fatalf("anthropic-beta changed to %q", got)
	}
	for name, values := range call.header {
		for _, v := range values {
			if strings.Contains(v, testPlaceholder) {
				t.Fatalf("provider received the placeholder in %s", name)
			}
		}
	}

	events := rec.waitAttempts(t, 2)
	if got := decisions(events); len(got) != 2 || got[0] != Forwarded || got[1] != Completed {
		t.Fatalf("decisions %v, want forwarded then completed", got)
	}
	done := events[1]
	if done.Status != http.StatusOK || done.SessionID != "session-1" || done.ResponseBytes == 0 || done.CredentialEchoed {
		t.Fatalf("completion event %+v", done)
	}
	evidence, _ := json.Marshal(events)
	for _, forbidden := range []string{"PROMPT-CONTENT-MARKER", "RESPONSE-CONTENT-MARKER", testSecret, testPlaceholder} {
		if strings.Contains(string(evidence), forbidden) {
			t.Fatalf("evidence contains %q: %s", forbidden, evidence)
		}
	}
}

func TestRefusesRequestsWithoutTheRunPlaceholder(t *testing.T) {
	cases := map[string]map[string]string{
		"no credential":         {},
		"other run placeholder": {"Authorization": "Bearer sk-ant-oat01-placeholder-run-2"},
		"real-looking token":    {"Authorization": "Bearer " + testSecret},
		"mismatched api key":    {"Authorization": "Bearer " + testPlaceholder, "X-Api-Key": "sk-ant-api03-other"},
		"api key only, foreign": {"X-Api-Key": "sk-ant-oat01-placeholder-run-2"},
	}
	for name, header := range cases {
		t.Run(name, func(t *testing.T) {
			fp := newFakeProvider(t, okResponse)
			rec := newRecorder()
			proxy := newTestProxy(t, fp, rec)
			resp := modelRequest(t, proxy.URL, http.MethodPost, "/v1/messages", "{}", header)
			assertRefused(t, resp, http.StatusForbidden)
			if fp.callCount() != 0 {
				t.Fatal("provider was called for a refused request")
			}
			if events := rec.waitAttempts(t, 1); len(events) != 1 || events[0].Decision != Refused || events[0].Reason != reasonCredential {
				t.Fatalf("evidence %+v", events)
			}
		})
	}
}

func TestRefusesNonModelRoutesEvenWithThePlaceholder(t *testing.T) {
	cases := []struct{ method, path string }{
		{http.MethodHead, "/api/hello"},
		{http.MethodGet, "/api/oauth/profile"},
		{http.MethodPost, "/api/oauth/claude_cli/create_api_key"},
		{http.MethodGet, "/v1/messages"},
		{http.MethodPost, "/v1/messages/../../api/oauth/profile"},
		{http.MethodPost, "/v1/messages/batches"},
	}
	for _, c := range cases {
		t.Run(c.method+" "+c.path, func(t *testing.T) {
			fp := newFakeProvider(t, okResponse)
			rec := newRecorder()
			proxy := newTestProxy(t, fp, rec)
			resp := modelRequest(t, proxy.URL, c.method, c.path, "", claudeHeaders())
			assertRefused(t, resp, http.StatusForbidden)
			if fp.callCount() != 0 {
				t.Fatal("provider was called for a non-model route")
			}
			if events := rec.waitAttempts(t, 1); len(events) != 1 || events[0].Reason != reasonRoute {
				t.Fatalf("evidence %+v", events)
			}
		})
	}
}

func TestEvidenceFailureStopsModelAccess(t *testing.T) {
	fp := newFakeProvider(t, okResponse)
	rec := failingAfter(0)
	proxy := newTestProxy(t, fp, rec)

	assertRefused(t, modelRequest(t, proxy.URL, http.MethodPost, "/v1/messages", "{}", claudeHeaders()), http.StatusServiceUnavailable)
	if fp.callCount() != 0 {
		t.Fatal("provider was called without durable evidence")
	}
}

func TestCompletionEvidenceFailureLatchesTheProxyClosed(t *testing.T) {
	fp := newFakeProvider(t, okResponse)
	rec := failingOnlyAttempt(2)
	proxy := newTestProxy(t, fp, rec)

	first := modelRequest(t, proxy.URL, http.MethodPost, "/v1/messages", "{}", claudeHeaders())
	_, _ = io.ReadAll(first.Body)
	if first.StatusCode != http.StatusOK {
		t.Fatalf("first request status %d", first.StatusCode)
	}
	rec.waitAttempts(t, 2)
	assertRefused(t, modelRequest(t, proxy.URL, http.MethodPost, "/v1/messages", "{}", claudeHeaders()), http.StatusServiceUnavailable)
	if fp.callCount() != 1 {
		t.Fatalf("provider saw %d calls after evidence failed, want 1", fp.callCount())
	}
}

func TestStreamsEventsWithoutBuffering(t *testing.T) {
	release := make(chan struct{})
	fp := newFakeProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: message_start\ndata: {}\n\n")
		w.(http.Flusher).Flush()
		<-release
		_, _ = io.WriteString(w, "event: message_stop\ndata: {}\n\n")
	})
	proxy := newTestProxy(t, fp, newRecorder())

	resp := modelRequest(t, proxy.URL, http.MethodPost, "/v1/messages", "{}", claudeHeaders())
	defer close(release)
	first := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(resp.Body).ReadString('\n')
		first <- line
	}()
	select {
	case line := <-first:
		if line != "event: message_start\n" {
			t.Fatalf("first streamed line %q", line)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first event did not arrive before the stream completed")
	}
}

func TestRecordsCredentialEchoedAcrossChunks(t *testing.T) {
	fp := newFakeProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "prefix "+testSecret[:20])
		w.(http.Flusher).Flush()
		_, _ = io.WriteString(w, testSecret[20:]+" suffix")
	})
	rec := newRecorder()
	proxy := newTestProxy(t, fp, rec)

	resp := modelRequest(t, proxy.URL, http.MethodPost, "/v1/messages", "{}", claudeHeaders())
	body, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(body), testSecret) || !strings.Contains(string(body), "[redacted]") {
		t.Fatalf("credential was not redacted from provider response")
	}
	if events := rec.waitAttempts(t, 2); len(events) != 2 || !events[1].CredentialEchoed {
		t.Fatalf("echo not recorded: %+v", events)
	}
}

func TestRejectsUnsafeConfiguration(t *testing.T) {
	good := Config{
		Upstream:    &url.URL{Scheme: "https", Host: "api.anthropic.com"},
		Placeholder: testPlaceholder,
		Credential:  Credential{secret: testSecret},
		Recorder:    newRecorder(),
	}
	if _, err := New(good); err != nil {
		t.Fatalf("valid configuration rejected: %v", err)
	}
	cases := map[string]func(c *Config){
		"plaintext upstream":     func(c *Config) { c.Upstream = &url.URL{Scheme: "http", Host: "api.anthropic.com"} },
		"missing upstream":       func(c *Config) { c.Upstream = nil },
		"missing credential":     func(c *Config) { c.Credential = Credential{} },
		"missing placeholder":    func(c *Config) { c.Placeholder = "" },
		"placeholder in secret":  func(c *Config) { c.Placeholder = testSecret[:20] },
		"missing evidence store": func(c *Config) { c.Recorder = nil },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			c := good
			mutate(&c)
			if _, err := New(c); err == nil {
				t.Fatal("expected configuration to be rejected")
			}
		})
	}
}

func assertRefused(t *testing.T, resp *http.Response, status int) {
	t.Helper()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != status {
		t.Fatalf("status %d, want %d (body %q)", resp.StatusCode, status, body)
	}
	if resp.Header.Get(shouldRetryHeader) != "false" {
		t.Fatal("refusal must tell the CLI not to retry")
	}
	if strings.Contains(string(body), testSecret) {
		t.Fatal("refusal body exposes the credential")
	}
}
