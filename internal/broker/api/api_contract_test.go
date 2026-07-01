package api

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCredentialRequestWireShape(t *testing.T) {
	body := CredentialRequest{
		SessionID:      "sess-1",
		BindSecret:     []byte("secret"),
		CredentialType: "github_app_installation",
		Resource:       "github:repo:maryzam/ai-crew-localdev",
		Params:         json.RawMessage(`{"permissions":{"contents":"write"}}`),
	}
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var parsed map[string]json.RawMessage
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	for _, key := range []string{"session_id", "bind_secret", "credential_type", "resource", "params"} {
		if _, ok := parsed[key]; !ok {
			t.Errorf("missing field %q in wire shape: %s", key, data)
		}
	}
	if got := string(parsed["credential_type"]); got != `"github_app_installation"` {
		t.Errorf("credential_type wire value = %s, want \"github_app_installation\"", got)
	}
}

func TestCredentialResponseWireShape(t *testing.T) {
	body := CredentialResponse{
		CredentialType: "github_app_installation",
		Resource:       "github:repo:maryzam/ai-crew-localdev",
		Credential:     json.RawMessage(`{"token":"ghs_xxx"}`),
	}
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var parsed map[string]json.RawMessage
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"credential_type", "resource", "credential", "expires_at"} {
		if _, ok := parsed[key]; !ok {
			t.Errorf("missing field %q in wire shape: %s", key, data)
		}
	}
}

func TestCreateSessionRequestUsesResources(t *testing.T) {
	body := CreateSessionRequest{
		AgentName:    "claude",
		HostRepoPath: "/home/dev/foo",
		Resources:    []string{"github:repo:maryzam/foo"},
		RunID:        "run_contract",
		TaskRef:      "github:owner/repo#43",
	}
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(data)
	if !strings.Contains(s, `"resources":["github:repo:maryzam/foo"]`) {
		t.Errorf("expected resources array in wire shape, got: %s", s)
	}
	if !strings.Contains(s, `"run_id":"run_contract"`) {
		t.Errorf("expected optional run_id in wire shape, got: %s", s)
	}
	if !strings.Contains(s, `"task_ref":"github:owner/repo#43"`) {
		t.Errorf("expected optional task_ref in wire shape, got: %s", s)
	}
	if strings.Contains(s, `"repo":`) {
		t.Errorf("CreateSessionRequest must not carry a singular \"repo\" field (legacy): %s", s)
	}
}

func TestParseResourceURI(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want ResourceURI
		err  bool
	}{
		{
			name: "github repo",
			in:   "github:repo:maryzam/ai-crew-localdev",
			want: ResourceURI{Provider: "github", Kind: "repo", Identifier: "maryzam/ai-crew-localdev"},
		},
		{
			name: "identifier with colons",
			in:   "aws:role:arn:aws:iam::123:role/x",
			want: ResourceURI{Provider: "aws", Kind: "role", Identifier: "arn:aws:iam::123:role/x"},
		},
		{name: "missing identifier", in: "github:repo", err: true},
		{name: "empty provider", in: ":repo:foo", err: true},
		{name: "empty kind", in: "github::foo", err: true},
		{name: "empty identifier", in: "github:repo:", err: true},
		{name: "no separators", in: "no-colons-at-all", err: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseResourceURI(tc.in)
			if tc.err {
				if err == nil {
					t.Errorf("ParseResourceURI(%q): expected error, got %+v", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Errorf("ParseResourceURI(%q): unexpected error: %v", tc.in, err)
				return
			}
			if got != tc.want {
				t.Errorf("ParseResourceURI(%q) = %+v, want %+v", tc.in, got, tc.want)
			}
			if got.String() != tc.in {
				t.Errorf("ResourceURI.String() round-trip: got %q, want %q", got.String(), tc.in)
			}
		})
	}
}
