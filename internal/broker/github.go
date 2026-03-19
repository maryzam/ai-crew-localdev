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
	// Extract repo name from "owner/repo".
	repoName := repo
	if idx := strings.Index(repo, "/"); idx >= 0 {
		repoName = repo[idx+1:]
	}

	reqBody := installationTokenRequest{
		Repositories: []string{repoName},
		Permissions:  permissions,
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
