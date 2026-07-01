package github

import (
	"strings"
	"testing"

	"github.com/maryzam/ai-crew-localdev/internal/broker/api"
)

func TestValidateResource(t *testing.T) {
	cases := []struct {
		name    string
		uri     api.ResourceURI
		wantErr string
	}{
		{
			name: "valid repo",
			uri:  api.ResourceURI{Provider: "github", Kind: "repo", Identifier: "owner/name"},
		},
		{
			name: "valid repo with hyphens dots underscores",
			uri:  api.ResourceURI{Provider: "github", Kind: "repo", Identifier: "my-org_1.5/my.repo-name"},
		},
		{
			name:    "wrong provider",
			uri:     api.ResourceURI{Provider: "aws", Kind: "repo", Identifier: "owner/name"},
			wantErr: `resource provider "aws"`,
		},
		{
			name:    "wrong kind",
			uri:     api.ResourceURI{Provider: "github", Kind: "org", Identifier: "owner"},
			wantErr: `resource kind "org"`,
		},
		{
			name:    "identifier missing slash",
			uri:     api.ResourceURI{Provider: "github", Kind: "repo", Identifier: "ownername"},
			wantErr: "invalid repo identifier",
		},
		{
			name:    "identifier with invalid characters",
			uri:     api.ResourceURI{Provider: "github", Kind: "repo", Identifier: "owner/name with spaces"},
			wantErr: "invalid repo identifier",
		},
		{
			name:    "identifier double slash",
			uri:     api.ResourceURI{Provider: "github", Kind: "repo", Identifier: "owner//name"},
			wantErr: "invalid repo identifier",
		},
		{
			name:    "identifier empty parts",
			uri:     api.ResourceURI{Provider: "github", Kind: "repo", Identifier: "/name"},
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
