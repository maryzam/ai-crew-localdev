package launcher

import (
	"testing"
)

func TestParseRemoteURL(t *testing.T) {
	tests := []struct {
		name    string
		remote  string
		want    string
		isSSH   bool
		wantErr bool
	}{
		{
			name:   "HTTPS with .git",
			remote: "https://github.com/owner/repo.git",
			want:   "owner/repo",
		},
		{
			name:   "HTTPS without .git",
			remote: "https://github.com/owner/repo",
			want:   "owner/repo",
		},
		{
			name:   "SSH with .git",
			remote: "git@github.com:owner/repo.git",
			want:   "owner/repo",
			isSSH:  true,
		},
		{
			name:   "SSH without .git",
			remote: "git@github.com:owner/repo",
			want:   "owner/repo",
			isSSH:  true,
		},
		{
			name:    "unsupported scheme",
			remote:  "ftp://github.com/owner/repo",
			wantErr: true,
		},
		{
			name:    "empty path",
			remote:  "https://github.com/",
			wantErr: true,
		},
		{
			name:    "no repo in path",
			remote:  "https://github.com/owner",
			wantErr: true,
		},
		{
			name:    "HTTP scheme rejected",
			remote:  "http://github.com/owner/repo",
			wantErr: true,
		},
		{
			name:    "non-GitHub HTTPS host rejected",
			remote:  "https://gitlab.com/owner/repo",
			wantErr: true,
		},
		{
			name:    "embedded HTTPS credentials rejected",
			remote:  "https://user:token@github.com/owner/repo.git",
			wantErr: true,
		},
		{
			name:    "SSH different host rejected",
			remote:  "git@gitlab.com:owner/repo.git",
			wantErr: true,
		},
		{
			name:    "HTTPS with extra path segments rejected",
			remote:  "https://github.com/owner/repo/extra",
			wantErr: true,
		},
		{
			name:    "SSH with extra path segments rejected",
			remote:  "git@github.com:owner/repo/extra.git",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, isSSH, err := ParseRemoteURL(tt.remote)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got slug=%q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("slug = %q, want %q", got, tt.want)
			}
			if isSSH != tt.isSSH {
				t.Errorf("isSSH = %v, want %v", isSSH, tt.isSSH)
			}
		})
	}
}

func TestResolveRepo(t *testing.T) {
	dir := t.TempDir()
	_, _, _, err := ResolveRepo(dir)
	if err == nil {
		t.Error("expected error for non-git directory")
	}
}
