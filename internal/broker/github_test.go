package broker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestGitHubClientMintInstallationToken(t *testing.T) {
	expires := time.Now().Add(time.Hour).Truncate(time.Second)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}

		wantPath := "/app/installations/42/access_tokens"
		if r.URL.Path != wantPath {
			t.Errorf("path = %s, want %s", r.URL.Path, wantPath)
		}

		auth := r.Header.Get("Authorization")
		if auth != "Bearer test-jwt" {
			t.Errorf("Authorization = %q, want Bearer test-jwt", auth)
		}

		// Verify request body.
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)

		repos, ok := body["repositories"].([]interface{})
		if !ok || len(repos) != 1 || repos[0] != "my-repo" {
			t.Errorf("repositories = %v, want [my-repo]", body["repositories"])
		}

		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"token":      "ghs_test123",
			"expires_at": expires.Format(time.RFC3339),
		})
	}))
	defer server.Close()

	client := NewGitHubClient(server.URL)
	resp, err := client.MintInstallationToken(
		context.Background(),
		"test-jwt",
		42,
		"owner/my-repo",
		map[string]string{"contents": "write"},
	)
	if err != nil {
		t.Fatalf("MintInstallationToken: %v", err)
	}

	if resp.Token != "ghs_test123" {
		t.Errorf("Token = %q, want ghs_test123", resp.Token)
	}
	if resp.Repo != "owner/my-repo" {
		t.Errorf("Repo = %q, want owner/my-repo", resp.Repo)
	}
}

func TestMintInstallationTokenNoRepo(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)

		if _, ok := body["repositories"]; ok {
			t.Error("expected no repositories field when repo is empty")
		}

		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"token":      "ghs_norepo",
			"expires_at": "2099-01-01T00:00:00Z",
		})
	}))
	defer server.Close()

	client := NewGitHubClient(server.URL)
	resp, err := client.MintInstallationToken(
		context.Background(), "test-jwt", 1, "", map[string]string{"metadata": "read"},
	)
	if err != nil {
		t.Fatalf("MintInstallationToken: %v", err)
	}
	if resp.Token != "ghs_norepo" {
		t.Errorf("Token = %q, want ghs_norepo", resp.Token)
	}
}

func TestListInstallations(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/app/installations" {
			t.Errorf("path = %s, want /app/installations", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer jwt-123" {
			t.Errorf("Authorization = %q, want Bearer jwt-123", r.Header.Get("Authorization"))
		}

		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode([]Installation{
			{ID: 10, Account: struct {
				Login string `json:"login"`
			}{Login: "org1"}},
			{ID: 20, Account: struct {
				Login string `json:"login"`
			}{Login: "org2"}},
		})
	}))
	defer server.Close()

	client := NewGitHubClient(server.URL)
	installs, err := client.ListInstallations(context.Background(), "jwt-123")
	if err != nil {
		t.Fatalf("ListInstallations: %v", err)
	}
	if len(installs) != 2 {
		t.Fatalf("got %d installations, want 2", len(installs))
	}
	if installs[0].Account.Login != "org1" {
		t.Errorf("installs[0].Account.Login = %q, want org1", installs[0].Account.Login)
	}
}

func TestListInstallationRepos(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "token tok-abc" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"repositories": []Repository{
				{FullName: "org/repo-a", Private: false},
				{FullName: "org/repo-b", Private: true},
			},
		})
	}))
	defer server.Close()

	client := NewGitHubClient(server.URL)
	repos, err := client.ListInstallationRepos(context.Background(), "tok-abc")
	if err != nil {
		t.Fatalf("ListInstallationRepos: %v", err)
	}
	if len(repos) != 2 {
		t.Fatalf("got %d repos, want 2", len(repos))
	}
	if repos[1].FullName != "org/repo-b" {
		t.Errorf("repos[1].FullName = %q, want org/repo-b", repos[1].FullName)
	}
}

func TestGitHubClientError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"Bad credentials"}`))
	}))
	defer server.Close()

	client := NewGitHubClient(server.URL)
	_, err := client.MintInstallationToken(
		context.Background(), "bad-jwt", 1, "o/r", nil,
	)
	if err == nil {
		t.Fatal("expected error for 401 response")
	}
}
