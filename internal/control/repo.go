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

type SSHRemoteError struct {
	RootPath string
	Slug     string
}

func (e *SSHRemoteError) HTTPSURL() string {
	return "https://github.com/" + e.Slug + ".git"
}

func (e *SSHRemoteError) Error() string {
	return fmt.Sprintf("repository %s uses an SSH remote; managed sessions require HTTPS remotes\nHint: git remote set-url origin %s", e.RootPath, e.HTTPSURL())
}

func ResolveRepository(repoPath string) (RepositoryResolution, error) {
	absPath, err := filepath.Abs(repoPath)
	if err != nil {
		return RepositoryResolution{}, fmt.Errorf("resolve absolute path: %w", err)
	}

	if out, err := exec.Command("git", "-C", absPath, "rev-parse", "--git-dir").CombinedOutput(); err != nil {
		return RepositoryResolution{}, fmt.Errorf("%s is not a git repository: %s", absPath, strings.TrimSpace(string(out)))
	}

	fetchURL, err := gitRemoteURL(absPath, false)
	if err != nil {
		return RepositoryResolution{}, err
	}
	slug, fetchSSH, err := ParseRemoteURL(fetchURL)
	if err != nil {
		return RepositoryResolution{}, fmt.Errorf("parse remote URL %q: %w", fetchURL, err)
	}
	pushSSH, err := pushRemoteIsSSH(absPath, fetchURL)
	if err != nil {
		return RepositoryResolution{}, err
	}

	return RepositoryResolution{RootPath: absPath, Slug: slug, Remote: fetchURL, SSH: fetchSSH || pushSSH}, nil
}

func gitRemoteURL(repoPath string, push bool) (string, error) {
	args := []string{"-C", repoPath, "remote", "get-url"}
	if push {
		args = append(args, "--push")
	}
	args = append(args, "origin")
	out, err := exec.Command("git", args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("no origin remote in %s: %s", repoPath, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func pushRemoteIsSSH(repoPath, fetchURL string) (bool, error) {
	pushURL, err := gitRemoteURL(repoPath, true)
	if err != nil {
		return false, err
	}
	if pushURL == fetchURL {
		return false, nil
	}
	if _, isSSH, parseErr := ParseRemoteURL(pushURL); parseErr == nil {
		return isSSH, nil
	}
	return false, nil
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
