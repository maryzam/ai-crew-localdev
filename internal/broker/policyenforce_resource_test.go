package broker

import (
	"errors"
	"testing"
)

func TestPolicyEnforcerAuthorizeResource(t *testing.T) {
	e := NewPolicyEnforcer(testPolicy(), "github")

	tests := []struct {
		name        string
		agent       string
		resource    ResourceURI
		wantErr     bool
		wantUnknown bool
	}{
		{
			name:     "github repo allowed",
			agent:    "claude",
			resource: ResourceURI{Provider: "github", Kind: "repo", Identifier: "owner/repo-a"},
		},
		{
			name:     "github repo not allowed",
			agent:    "claude",
			resource: ResourceURI{Provider: "github", Kind: "repo", Identifier: "owner/repo-x"},
			wantErr:  true,
		},
		{
			name:     "unknown agent",
			agent:    "codex",
			resource: ResourceURI{Provider: "github", Kind: "repo", Identifier: "owner/repo-a"},
			wantErr:  true,
		},
		{
			name:        "non-github provider",
			agent:       "claude",
			resource:    ResourceURI{Provider: "aws", Kind: "iam", Identifier: "arn:aws:iam::123:role/foo"},
			wantErr:     true,
			wantUnknown: true,
		},
		{
			name:     "github non-repo kind not in resources",
			agent:    "claude",
			resource: ResourceURI{Provider: "github", Kind: "org", Identifier: "owner"},
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := e.AuthorizeResource(tt.agent, tt.resource)
			if (err != nil) != tt.wantErr {
				t.Fatalf("AuthorizeResource err = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantUnknown && !errors.Is(err, ErrUnknownCredentialType) {
				t.Errorf("err = %v, want errors.Is(ErrUnknownCredentialType)", err)
			}
		})
	}
}
