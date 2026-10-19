package modelproxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func testModelHandlers(t *testing.T, provider *fakeProvider, recorder Recorder) map[string]struct {
	handler     http.Handler
	path        string
	placeholder string
	secret      string
} {
	t.Helper()
	upstream, err := url.Parse(provider.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	claude, err := New(Config{Upstream: upstream, Placeholder: testPlaceholder, Credential: Credential{secret: testSecret}, Recorder: recorder, Transport: provider.server.Client().Transport})
	if err != nil {
		t.Fatal(err)
	}
	codex, err := NewCodex(CodexConfig{Upstream: upstream, Placeholder: codexPlaceholder, Auth: &fakeCodexSource{credential: CodexCredential{accessToken: codexAccess, accountID: "acct_host"}}, Recorder: recorder, Transport: provider.server.Client().Transport})
	if err != nil {
		t.Fatal(err)
	}
	return map[string]struct {
		handler     http.Handler
		path        string
		placeholder string
		secret      string
	}{
		"claude": {handler: claude, path: "/v1/messages", placeholder: testPlaceholder, secret: testSecret},
		"codex":  {handler: codex, path: codexPrefix + "/responses", placeholder: codexPlaceholder, secret: codexAccess},
	}
}

func TestBothProxiesRejectCredentialSmuggling(t *testing.T) {
	for _, variant := range []string{"missing credential", "foreign placeholder", "duplicate authorization", "foreign api key", "foreign account", "cookie", "encoded path", "non-model route", "wrong method"} {
		t.Run(variant, func(t *testing.T) {
			provider := newFakeProvider(t, okResponse)
			recorder := newRecorder()
			for name, tc := range testModelHandlers(t, provider, recorder) {
				t.Run(name, func(t *testing.T) {
					request := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader("{}"))
					request.Header.Set("Authorization", "Bearer "+tc.placeholder)
					switch variant {
					case "missing credential":
						request.Header.Del("Authorization")
					case "foreign placeholder":
						request.Header.Set("Authorization", "Bearer another-run-placeholder")
					case "duplicate authorization":
						request.Header.Add("Authorization", "Bearer "+tc.placeholder)
					case "foreign api key":
						request.Header.Set("X-Api-Key", "foreign-credential")
					case "foreign account":
						request.Header.Set("ChatGPT-Account-ID", "other-account")
					case "cookie":
						request.Header.Set("Cookie", "host-session=unrelated")
					case "encoded path":
						request.URL.RawPath = strings.Replace(tc.path, "/", "%2f", 1)
					case "non-model route":
						request.URL.Path = "/PROMPT-CONTENT-MARKER"
					case "wrong method":
						request.Method = http.MethodGet
					}
					response := httptest.NewRecorder()
					tc.handler.ServeHTTP(response, request)
					if response.Code != http.StatusForbidden {
						t.Fatalf("proxy returned %d", response.Code)
					}
				})
			}
			if provider.callCount() != 0 {
				t.Fatal("refused request reached the model provider")
			}
			if variant == "non-model route" {
				for _, event := range recorder.waitAttempts(t, 2) {
					if event.Path != "other" {
						t.Fatalf("untrusted path reached evidence: %+v", event)
					}
				}
			}
		})
	}
}

func TestBothProxiesRequireHTTPSUpstream(t *testing.T) {
	upstream, _ := url.Parse("http://model.example")
	if _, err := New(Config{Upstream: upstream, Placeholder: testPlaceholder, Credential: Credential{secret: testSecret}, Recorder: newRecorder()}); err == nil {
		t.Fatal("Claude accepted a plaintext upstream")
	}
	if _, err := NewCodex(CodexConfig{Upstream: upstream, Placeholder: codexPlaceholder, Auth: &fakeCodexSource{}, Recorder: newRecorder()}); err == nil {
		t.Fatal("Codex accepted a plaintext upstream")
	}
}

func TestBothProxiesRedactResponseHeadersAndAuditMetadata(t *testing.T) {
	for _, name := range []string{"claude", "codex"} {
		t.Run(name, func(t *testing.T) {
			secret := testSecret
			if name == "codex" {
				secret = codexAccess
			}
			provider := newFakeProvider(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("X-Reflected-Token", "Bearer "+secret)
				w.Header().Set("Set-Cookie", "token="+secret)
				w.Header().Set("Trailer", "X-Secret-Trailer")
				_, _ = io.WriteString(w, "response "+secret)
				w.Header().Set("X-Secret-Trailer", secret)
			})
			recorder := newRecorder()
			tc := testModelHandlers(t, provider, recorder)[name]
			request := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader("{}"))
			request.Header.Set("Authorization", "Bearer "+tc.placeholder)
			request.Header.Set("User-Agent", "content "+secret)
			request.Header.Set(sessionHeader, "content "+secret)
			response := httptest.NewRecorder()
			tc.handler.ServeHTTP(response, request)
			if response.Code != http.StatusOK || strings.Contains(response.Body.String(), secret) || strings.Contains(response.Header().Get("X-Reflected-Token"), secret) || response.Header().Get("Set-Cookie") != "" || response.Header().Get("Trailer") != "" || response.Result().Trailer.Get("X-Secret-Trailer") != "" {
				t.Fatalf("response boundary failed: status=%d body=%t header=%t cookie=%t trailer declaration=%t trailer value=%t", response.Code, strings.Contains(response.Body.String(), secret), strings.Contains(response.Header().Get("X-Reflected-Token"), secret), response.Header().Get("Set-Cookie") != "", response.Header().Get("Trailer") != "", response.Result().Trailer.Get("X-Secret-Trailer") != "")
			}
			events := recorder.waitAttempts(t, 2)
			if events[0].SessionID != "" || !events[1].CredentialEchoed {
				t.Fatalf("unsafe or missing audit metadata: %+v", events)
			}
		})
	}
}
