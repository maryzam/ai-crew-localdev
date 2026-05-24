package github

import (
	"strings"
	"testing"

	"github.com/maryzam/ai-crew-localdev/internal/broker"
)

func TestValidateResource(t *testing.T) {
	cases := []struct {
		name    string
		uri     broker.ResourceURI
		wantErr string
	}{
		{
			name: "valid repo",
			uri:  broker.ResourceURI{Provider: "github", Kind: "repo", Identifier: "owner/name"},
		},
		{
			name: "valid repo with hyphens dots underscores",
			uri:  broker.ResourceURI{Provider: "github", Kind: "repo", Identifier: "my-org_1.5/my.repo-name"},
		},
		{
			name:    "wrong provider",
			uri:     broker.ResourceURI{Provider: "aws", Kind: "repo", Identifier: "owner/name"},
			wantErr: `resource provider "aws"`,
		},
		{
			name:    "wrong kind",
			uri:     broker.ResourceURI{Provider: "github", Kind: "org", Identifier: "owner"},
			wantErr: `resource kind "org"`,
		},
		{
			name:    "identifier missing slash",
			uri:     broker.ResourceURI{Provider: "github", Kind: "repo", Identifier: "ownername"},
			wantErr: "invalid repo identifier",
		},
		{
			name:    "identifier with invalid characters",
			uri:     broker.ResourceURI{Provider: "github", Kind: "repo", Identifier: "owner/name with spaces"},
			wantErr: "invalid repo identifier",
		},
		{
			name:    "identifier double slash",
			uri:     broker.ResourceURI{Provider: "github", Kind: "repo", Identifier: "owner//name"},
			wantErr: "invalid repo identifier",
		},
		{
			name:    "identifier empty parts",
			uri:     broker.ResourceURI{Provider: "github", Kind: "repo", Identifier: "/name"},
			wantErr: "invalid repo identifier",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateResource(tc.uri)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error = %v, want substring %q", err, tc.wantErr)
			}
		})
	}
}
