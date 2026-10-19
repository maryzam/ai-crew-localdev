package modelproxy

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

const codexPlaceholder = "ai-crew-run-placeholder-test-1234567890"
const codexAccess = "TEST_HOST_ACCESS_TOKEN"

type fakeCodexSource struct {
	credential CodexCredential
	err        error
	resolved   int
	invalid    bool
}

func (s *fakeCodexSource) Resolve(_ context.Context) (CodexCredential, error) {
	s.resolved++
	return s.credential, s.err
}

func (s *fakeCodexSource) Invalidate(string) { s.invalid = true }

func newTestCodexProxy(t *testing.T, provider *fakeProvider, source CodexCredentialSource, recorder Recorder) *httptest.Server {
	t.Helper()
	upstream, err := url.Parse(provider.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	proxy, err := NewCodex(CodexConfig{Upstream: upstream, Placeholder: codexPlaceholder, Auth: source, Recorder: recorder, Transport: provider.server.Client().Transport})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(proxy)
	t.Cleanup(server.Close)
	return server
}

func codexHeaders() map[string]string {
	return map[string]string{"Authorization": "Bearer " + codexPlaceholder, "User-Agent": "codex-cli/0.163.0"}
}

func TestCodexProxyForwardsOnlyHostCredentialAndAccount(t *testing.T) {
	provider := newFakeProvider(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: RESPONSE-CONTENT-MARKER\n\n")
	})
	source := &fakeCodexSource{credential: CodexCredential{accessToken: codexAccess, accountID: "acct_host"}}
	recorder := newRecorder()
	proxy := newTestCodexProxy(t, provider, source, recorder)
	response := modelRequest(t, proxy.URL, http.MethodPost, codexPrefix+"/responses", `{"input":"PROMPT-CONTENT-MARKER"}`, codexHeaders())
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK || !strings.Contains(string(body), "RESPONSE-CONTENT-MARKER") {
		t.Fatalf("status %d body %q", response.StatusCode, body)
	}
	calls := provider.received()
	if len(calls) != 1 || calls[0].uri != codexPrefix+"/responses" {
		t.Fatalf("upstream calls %+v", calls)
	}
	if got := calls[0].header.Get("Authorization"); got != "Bearer "+codexAccess {
		t.Fatal("host access credential was not substituted")
	}
	if got := calls[0].header.Get("ChatGPT-Account-ID"); got != "acct_host" {
		t.Fatalf("account ID %q", got)
	}
	events := recorder.waitAttempts(t, 2)
	if got := decisions(events); len(got) != 2 || got[0] != Forwarded || got[1] != Completed {
		t.Fatalf("decisions %v", got)
	}
	evidence, _ := json.Marshal(events)
	for _, forbidden := range []string{"PROMPT-CONTENT-MARKER", "RESPONSE-CONTENT-MARKER", codexAccess, codexPlaceholder} {
		if strings.Contains(string(evidence), forbidden) {
			t.Fatalf("evidence contained sensitive content")
		}
	}
}

func TestCodexProxyFailsClosedWhenEvidenceOrHostLoginFails(t *testing.T) {
	provider := newFakeProvider(t, okResponse)
	source := &fakeCodexSource{credential: CodexCredential{accessToken: codexAccess, accountID: "acct_host"}}
	proxy := newTestCodexProxy(t, provider, source, failingAfter(0))
	response := modelRequest(t, proxy.URL, http.MethodPost, codexPrefix+"/responses", "{}", codexHeaders())
	assertRefused(t, response, http.StatusServiceUnavailable)
	if provider.callCount() != 0 {
		t.Fatal("request was forwarded without evidence")
	}
	response = modelRequest(t, proxy.URL, http.MethodPost, codexPrefix+"/responses", "{}", codexHeaders())
	assertRefused(t, response, http.StatusServiceUnavailable)
	if source.resolved != 1 {
		t.Fatal("broken evidence did not latch before credential access")
	}

	provider2 := newFakeProvider(t, okResponse)
	source2 := &fakeCodexSource{err: errors.New("host login unavailable")}
	recorder := newRecorder()
	proxy2 := newTestCodexProxy(t, provider2, source2, recorder)
	response = modelRequest(t, proxy2.URL, http.MethodPost, codexPrefix+"/responses", "{}", codexHeaders())
	assertRefused(t, response, http.StatusServiceUnavailable)
	if provider2.callCount() != 0 {
		t.Fatal("request was forwarded without host login")
	}
	provider3 := newFakeProvider(t, okResponse)
	source3 := &fakeCodexSource{credential: CodexCredential{accessToken: codexPlaceholder, accountID: "acct_host"}}
	proxy3 := newTestCodexProxy(t, provider3, source3, newRecorder())
	response = modelRequest(t, proxy3.URL, http.MethodPost, codexPrefix+"/responses", "{}", codexHeaders())
	assertRefused(t, response, http.StatusServiceUnavailable)
	if provider3.callCount() != 0 {
		t.Fatal("placeholder was accepted as the host credential")
	}
}

func TestCodexProxyRedactsCredentialEchoAndInvalidatesOn401(t *testing.T) {
	provider := newFakeProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Www-Authenticate", `Bearer error="invalid_token"`)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, "prefix "+codexAccess[:8])
		w.(http.Flusher).Flush()
		_, _ = io.WriteString(w, codexAccess[8:]+" suffix")
	})
	source := &fakeCodexSource{credential: CodexCredential{accessToken: codexAccess, accountID: "acct_host"}}
	recorder := newRecorder()
	proxy := newTestCodexProxy(t, provider, source, recorder)
	response := modelRequest(t, proxy.URL, http.MethodPost, codexPrefix+"/responses", "{}", codexHeaders())
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusUnauthorized || strings.Contains(string(body), codexAccess) || !strings.Contains(string(body), "[redacted]") || !source.invalid {
		t.Fatal("401 did not invalidate and redact the host credential")
	}
	if events := recorder.waitAttempts(t, 3); events[1].Decision != Invalidated || !events[2].CredentialEchoed {
		t.Fatal("credential echo was not recorded")
	}
}

func TestCodexProxyRejectsUninspectableUpstreamBody(t *testing.T) {
	provider := newFakeProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = io.WriteString(w, codexAccess)
	})
	source := &fakeCodexSource{credential: CodexCredential{accessToken: codexAccess, accountID: "acct_host"}}
	proxy := newTestCodexProxy(t, provider, source, newRecorder())
	response := modelRequest(t, proxy.URL, http.MethodPost, codexPrefix+"/responses", "{}", codexHeaders())
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusBadGateway || strings.Contains(string(body), codexAccess) {
		t.Fatal("encoded upstream response reached the agent")
	}
}
