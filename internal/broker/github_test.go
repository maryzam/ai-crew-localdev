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
		json.NewDecoder(r.Body).Decode(&body)

		repos, ok := body["repositories"].([]interface{})
		if !ok || len(repos) != 1 || repos[0] != "my-repo" {
			t.Errorf("repositories = %v, want [my-repo]", body["repositories"])
		}

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]interface{}{
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

func TestGitHubClientError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"message":"Bad credentials"}`))
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
