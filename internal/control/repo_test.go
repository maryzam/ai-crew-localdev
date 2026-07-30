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

func TestSSHRemoteErrorURLAndMessage(t *testing.T) {
	err := &SSHRemoteError{RootPath: "/repo", Slug: "owner/repo"}
	if err.HTTPSURL() != "https://github.com/owner/repo.git" {
		t.Fatalf("HTTPSURL = %q", err.HTTPSURL())
	}
	if !strings.Contains(err.Error(), "uses an SSH remote") {
		t.Fatalf("message = %q", err.Error())
	}
}
