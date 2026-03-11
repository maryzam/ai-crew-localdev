// Package launcher implements the session launch logic for "ai-agent run".
//
// It handles repository resolution from git remotes, ambient credential
// scrubbing, memfd-based secret delivery, and agent process execution.
package launcher

import (
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// sshRemoteRe matches git@github.com:owner/repo.git style remotes.
var sshRemoteRe = regexp.MustCompile(`^git@([^:]+):(.+?)(?:\.git)?$`)

// ResolveRepo resolves a local repository path to its owner/repo slug
// by reading the origin remote URL. It returns the absolute host path,
// the owner/repo slug, and whether the remote uses SSH.
func ResolveRepo(repoPath string) (absPath string, slug string, isSSH bool, err error) {
	absPath, err = filepath.Abs(repoPath)
	if err != nil {
		return "", "", false, fmt.Errorf("resolve absolute path: %w", err)
	}

	// Verify it's a git repo.
	cmd := exec.Command("git", "-C", absPath, "rev-parse", "--git-dir")
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", "", false, fmt.Errorf("%s is not a git repository: %s", absPath, strings.TrimSpace(string(out)))
	}

	// Get origin remote URL.
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

// ParseRemoteURL extracts owner/repo from a git remote URL.
// Supports HTTPS (https://github.com/owner/repo.git) and
// SSH (git@github.com:owner/repo.git) formats.
func ParseRemoteURL(remote string) (slug string, isSSH bool, err error) {
	// Try SSH format first.
	if m := sshRemoteRe.FindStringSubmatch(remote); m != nil {
		slug = strings.TrimSuffix(m[2], ".git")
		return slug, true, nil
	}

	// Try HTTPS format.
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

	// Path is /owner/repo or /owner/repo.git
	path := strings.TrimPrefix(u.Path, "/")
	path = strings.TrimSuffix(path, ".git")

	parts := strings.SplitN(path, "/", 3)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", false, fmt.Errorf("cannot extract owner/repo from path %q", u.Path)
	}

	return parts[0] + "/" + parts[1], false, nil
}
