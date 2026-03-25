package broker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// GitHubClient mints installation access tokens via the GitHub REST API.
type GitHubClient struct {
	httpClient *http.Client
	baseURL    string // e.g., "https://api.github.com"
}

// NewGitHubClient creates a client for the GitHub API. If baseURL is
// empty, "https://api.github.com" is used.
func NewGitHubClient(baseURL string) *GitHubClient {
	if baseURL == "" {
		baseURL = "https://api.github.com"
	}
	return &GitHubClient{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		baseURL:    strings.TrimRight(baseURL, "/"),
	}
}

// installationTokenRequest is the body for the GitHub API.
type installationTokenRequest struct {
	Repositories []string          `json:"repositories,omitempty"`
	Permissions  map[string]string `json:"permissions,omitempty"`
}

// installationTokenResponse is the relevant subset of GitHub's response.
type installationTokenResponse struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// MintInstallationToken exchanges a JWT for a scoped installation access token.
// repo should be "owner/repo"; the repo name portion is extracted for the API.
func (c *GitHubClient) MintInstallationToken(
	ctx context.Context,
	jwt string,
	installationID int64,
	repo string,
	permissions map[string]string,
) (*TokenResponse, error) {
	reqBody := installationTokenRequest{
		Permissions: permissions,
	}

	// Only scope to a specific repo if one was provided.
	if repo != "" {
		repoName := repo
		if idx := strings.Index(repo, "/"); idx >= 0 {
			repoName = repo[idx+1:]
		}
		reqBody.Repositories = []string{repoName}
	}

	bodyJSON, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("github: marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/app/installations/%d/access_tokens", c.baseURL, installationID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyJSON))
	if err != nil {
		return nil, fmt.Errorf("github: create request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MB max
	if err != nil {
		return nil, fmt.Errorf("github: read response: %w", err)
	}

	if resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("github: unexpected status %d: %s", resp.StatusCode, body)
	}

	var tokenResp installationTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("github: unmarshal response: %w", err)
	}

	return &TokenResponse{
		Token:     tokenResp.Token,
		ExpiresAt: tokenResp.ExpiresAt,
		Repo:      repo,
	}, nil
}

// Installation represents a GitHub App installation.
type Installation struct {
	ID      int64  `json:"id"`
	Account struct {
		Login string `json:"login"`
	} `json:"account"`
	RepositorySelection string `json:"repository_selection"` // "all" or "selected"
}

// ListInstallations returns all installations for the authenticated GitHub App.
// Authenticates with a JWT (not an installation token).
func (c *GitHubClient) ListInstallations(ctx context.Context, jwt string) ([]Installation, error) {
	url := c.baseURL + "/app/installations"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("github: create request: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+jwt)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("github: request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("github: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("github: unexpected status %d: %s", resp.StatusCode, body)
	}

	var installations []Installation
	if err := json.Unmarshal(body, &installations); err != nil {
		return nil, fmt.Errorf("github: unmarshal installations: %w", err)
	}
	return installations, nil
}

// Repository is a minimal representation of a GitHub repository.
type Repository struct {
	FullName string `json:"full_name"` // "owner/repo"
	Private  bool   `json:"private"`
}

type listReposResponse struct {
	Repositories []Repository `json:"repositories"`
}

// ListInstallationRepos returns repositories accessible to the given installation token.
func (c *GitHubClient) ListInstallationRepos(ctx context.Context, token string) ([]Repository, error) {
	var all []Repository
	page := 1
	for {
		url := fmt.Sprintf("%s/installation/repositories?per_page=100&page=%d", c.baseURL, page)
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, fmt.Errorf("github: create request: %w", err)
		}
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("Authorization", "token "+token)
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("github: request failed: %w", err)
		}

		body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		_ = resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("github: read response: %w", err)
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("github: unexpected status %d: %s", resp.StatusCode, body)
		}

		var result listReposResponse
		if err := json.Unmarshal(body, &result); err != nil {
			return nil, fmt.Errorf("github: unmarshal repos: %w", err)
		}
		all = append(all, result.Repositories...)
		if len(result.Repositories) < 100 {
			break
		}
		page++
	}
	return all, nil
}
