package control

import (
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var sshRemoteRe = regexp.MustCompile(`^git@([^:]+):(.+?)(?:\.git)?$`)

type RepositoryResolution struct {
	RootPath string
	Slug     string
	Remote   string
	SSH      bool
}

func ResolveRepository(repoPath string) (RepositoryResolution, error) {
	absPath, err := filepath.Abs(repoPath)
	if err != nil {
		return RepositoryResolution{}, fmt.Errorf("resolve absolute path: %w", err)
	}

	cmd := exec.Command("git", "-C", absPath, "rev-parse", "--git-dir")
	if out, err := cmd.CombinedOutput(); err != nil {
		return RepositoryResolution{}, fmt.Errorf("%s is not a git repository: %s", absPath, strings.TrimSpace(string(out)))
	}

	cmd = exec.Command("git", "-C", absPath, "remote", "get-url", "origin")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return RepositoryResolution{}, fmt.Errorf("no origin remote in %s: %s", absPath, strings.TrimSpace(string(out)))
	}

	remote := strings.TrimSpace(string(out))
	slug, isSSH, err := ParseRemoteURL(remote)
	if err != nil {
		return RepositoryResolution{}, fmt.Errorf("parse remote URL %q: %w", remote, err)
	}

	return RepositoryResolution{RootPath: absPath, Slug: slug, Remote: remote, SSH: isSSH}, nil
}

func ParseRemoteURL(remote string) (slug string, isSSH bool, err error) {
	if m := sshRemoteRe.FindStringSubmatch(remote); m != nil {
		if m[1] != "github.com" {
			return "", false, fmt.Errorf("unsupported SSH host %q (only github.com is supported)", m[1])
		}
		slug, err := parseRepoPath(m[2])
		if err != nil {
			return "", false, err
		}
		return slug, true, nil
	}

	u, err := url.Parse(remote)
	if err != nil {
		return "", false, fmt.Errorf("not a valid URL: %w", err)
	}
	if u.Scheme != "https" {
		return "", false, fmt.Errorf("unsupported remote scheme %q (only https is supported)", u.Scheme)
	}
	if u.Host != "github.com" {
		return "", false, fmt.Errorf("unsupported host %q (only github.com is supported)", u.Host)
	}
	if u.User != nil {
		return "", false, fmt.Errorf("remote must not embed credentials")
	}

	slug, err = parseRepoPath(u.Path)
	if err != nil {
		return "", false, err
	}
	return slug, false, nil
}

func parseRepoPath(path string) (string, error) {
	path = strings.TrimPrefix(path, "/")
	path = strings.TrimSuffix(path, ".git")

	parts := strings.Split(path, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", fmt.Errorf("cannot extract owner/repo from path %q", path)
	}
	return parts[0] + "/" + parts[1], nil
}
