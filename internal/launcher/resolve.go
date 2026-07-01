package launcher

import (
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var sshRemoteRe = regexp.MustCompile(`^git@([^:]+):(.+?)(?:\.git)?$`)

func ResolveRepo(repoPath string) (absPath string, slug string, isSSH bool, err error) {
	absPath, err = filepath.Abs(repoPath)
	if err != nil {
		return "", "", false, fmt.Errorf("resolve absolute path: %w", err)
	}

	cmd := exec.Command("git", "-C", absPath, "rev-parse", "--git-dir")
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", "", false, fmt.Errorf("%s is not a git repository: %s", absPath, strings.TrimSpace(string(out)))
	}

	cmd = exec.Command("git", "-C", absPath, "remote", "get-url", "origin")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", "", false, fmt.Errorf("no origin remote in %s: %s", absPath, strings.TrimSpace(string(out)))
	}

	remoteURL := strings.TrimSpace(string(out))

	slug, isSSH, err = ParseRemoteURL(remoteURL)
	if err != nil {
		return "", "", false, fmt.Errorf("parse remote URL %q: %w", remoteURL, err)
	}

	return absPath, slug, isSSH, nil
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
