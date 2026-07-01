package github

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	githubcontract "github.com/maryzam/ai-crew-localdev/internal/providers/github/contract"
)

type GitHubClient struct {
	httpClient *http.Client
	baseURL    string
}

func NewGitHubClient(baseURL string) *GitHubClient {
	if baseURL == "" {
		baseURL = "https://api.github.com"
	}
	return &GitHubClient{
		httpClient: &http.Client{Timeout: 30 * time.Second},
		baseURL:    strings.TrimRight(baseURL, "/"),
	}
}

type installationTokenRequest struct {
	Repositories []string          `json:"repositories,omitempty"`
	Permissions  map[string]string `json:"permissions,omitempty"`
}

type installationTokenResponse struct {
	Token     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (c *GitHubClient) MintInstallationToken(
	ctx context.Context,
	jwt string,
	installationID int64,
	repo string,
	permissions map[string]string,
) (*githubcontract.InstallationToken, error) {
	reqBody := installationTokenRequest{
		Permissions: permissions,
	}

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

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
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

	return &githubcontract.InstallationToken{
		Token:     tokenResp.Token,
		ExpiresAt: tokenResp.ExpiresAt,
		Repo:      repo,
	}, nil
}

func (c *GitHubClient) ListInstallations(ctx context.Context, jwt string) ([]githubcontract.Installation, error) {
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

	var installations []githubcontract.Installation
	if err := json.Unmarshal(body, &installations); err != nil {
		return nil, fmt.Errorf("github: unmarshal installations: %w", err)
	}
	return installations, nil
}

type listReposResponse struct {
	Repositories []githubcontract.Repository `json:"repositories"`
}

func (c *GitHubClient) ListInstallationRepos(ctx context.Context, token string) ([]githubcontract.Repository, error) {
	var all []githubcontract.Repository
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
