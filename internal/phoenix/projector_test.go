package phoenix

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProjectorAcceptsOnlyLoopbackAndProjectsOnlyTraces(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		requests++
		if request.URL.Path != "/v1/traces" || request.Header.Get("Content-Type") != "application/json" {
			t.Errorf("request = %s %s", request.Method, request.URL.Path)
		}
		response.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	projector, err := NewProjector(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if attempted, err := projector.Project(context.Background(), "logs", []byte(`{}`)); attempted || err != nil {
		t.Fatalf("logs projection = %t, %v", attempted, err)
	}
	if attempted, err := projector.Project(context.Background(), "traces", []byte(`{"resourceSpans":[]}`)); !attempted || err != nil {
		t.Fatalf("trace projection = %t, %v", attempted, err)
	}
	if requests != 1 {
		t.Fatalf("requests = %d, want 1", requests)
	}
	for _, endpoint := range []string{"https://127.0.0.1:4319", "http://localhost:4319", "http://192.0.2.1:4319", "http://127.0.0.1:4319/path"} {
		if _, err := NewProjector(endpoint); err == nil {
			t.Fatalf("accepted unsafe endpoint %q", endpoint)
		}
	}
}

func TestProjectorReportsCollectorFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		http.Error(response, "unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	projector, err := NewProjector(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	attempted, err := projector.Project(context.Background(), "traces", []byte(`{}`))
	if !attempted || err == nil || !strings.Contains(err.Error(), "503") {
		t.Fatalf("projection failure = %t, %v", attempted, err)
	}
}
