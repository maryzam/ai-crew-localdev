package control

import (
	"strings"
	"testing"
)

func TestResolveRepositoryDetectsSSHPushURL(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init", "-q")
	runGit(t, repo, "remote", "add", "origin", "https://github.com/owner/repo.git")
	runGit(t, repo, "remote", "set-url", "--push", "origin", "git@github.com:owner/repo.git")

	resolution, err := ResolveRepository(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !resolution.SSH {
		t.Fatal("HTTPS fetch with an SSH push url must resolve as SSH")
	}
	if resolution.Slug != "owner/repo" {
		t.Fatalf("slug = %q, want owner/repo", resolution.Slug)
	}
}

func TestResolveRepositoryHTTPSFetchAndPushIsNotSSH(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init", "-q")
	runGit(t, repo, "remote", "add", "origin", "https://github.com/owner/repo.git")

	resolution, err := ResolveRepository(repo)
	if err != nil {
		t.Fatal(err)
	}
	if resolution.SSH {
		t.Fatal("HTTPS fetch and push must not resolve as SSH")
	}
}

func TestResolveRepositoryFailsClosedOnSSHSchemePushURL(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init", "-q")
	runGit(t, repo, "remote", "add", "origin", "https://github.com/owner/repo.git")
	runGit(t, repo, "remote", "set-url", "--push", "origin", "ssh://git@github.com/owner/repo.git")

	resolution, err := ResolveRepository(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !resolution.SSH {
		t.Fatal("an ssh:// push url must fail closed and resolve as SSH")
	}
}

func TestResolveRepositoryDetectsAdditionalSSHPushURL(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init", "-q")
	runGit(t, repo, "remote", "add", "origin", "https://github.com/owner/repo.git")
	runGit(t, repo, "remote", "set-url", "--add", "--push", "origin", "https://github.com/owner/repo.git")
	runGit(t, repo, "remote", "set-url", "--add", "--push", "origin", "git@github.com:owner/repo.git")

	resolution, err := ResolveRepository(repo)
	if err != nil {
		t.Fatal(err)
	}
	if !resolution.SSH {
		t.Fatal("an additional SSH push url must be detected, not just the default")
	}
}

func TestResolveRepositoryClassifiesSSHURLFetchRemote(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init", "-q")
	runGit(t, repo, "remote", "add", "origin", "ssh://git@github.com/owner/repo.git")

	resolution, err := ResolveRepository(repo)
	if err != nil {
		t.Fatalf("ssh:// fetch remote should classify, not error: %v", err)
	}
	if !resolution.SSH || resolution.Slug != "owner/repo" {
		t.Fatalf("resolution = %+v, want SSH=true slug=owner/repo", resolution)
	}
}

func TestParseRemoteURLClassifiesSSHAndHTTPS(t *testing.T) {
	cases := []struct {
		remote string
		slug   string
		ssh    bool
	}{
		{"git@github.com:owner/repo.git", "owner/repo", true},
		{"ssh://git@github.com/owner/repo.git", "owner/repo", true},
		{"ssh://git@github.com:22/owner/repo.git", "owner/repo", true},
		{"https://github.com/owner/repo.git", "owner/repo", false},
	}
	for _, tc := range cases {
		slug, ssh, err := ParseRemoteURL(tc.remote)
		if err != nil {
			t.Fatalf("%s: unexpected error %v", tc.remote, err)
		}
		if slug != tc.slug || ssh != tc.ssh {
			t.Fatalf("%s: got (%q, %v), want (%q, %v)", tc.remote, slug, ssh, tc.slug, tc.ssh)
		}
	}
}

func TestSSHRemoteErrorURLAndMessage(t *testing.T) {
	err := &SSHRemoteError{RootPath: "/repo", Slug: "owner/repo"}
	if err.HTTPSURL() != "https://github.com/owner/repo.git" {
		t.Fatalf("HTTPSURL = %q", err.HTTPSURL())
	}
	if !strings.Contains(err.Error(), "uses an SSH remote") {
		t.Fatalf("message = %q", err.Error())
	}
}
