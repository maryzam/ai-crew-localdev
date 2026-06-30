package brokerclient

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/maryzam/ai-crew-localdev/internal/brokerapi"
)

// fakeServer accepts one connection, reads a request, and writes a canned response.
func fakeServer(t *testing.T, socketPath string, handler func(brokerapi.Request) brokerapi.Response) {
	t.Helper()

	ln, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()

		var req brokerapi.Request
		if err := json.NewDecoder(conn).Decode(&req); err != nil {
			return
		}

		resp := handler(req)
		_ = json.NewEncoder(conn).Encode(resp)
	}()
}

func mustMarshal(t *testing.T, v interface{}) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return b
}

func TestCreateSession(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "broker.sock")

	want := brokerapi.CreateSessionResponse{
		SessionID:  "test-session-123",
		BindSecret: []byte("secret-bytes"),
	}

	fakeServer(t, sock, func(req brokerapi.Request) brokerapi.Response {
		if req.Method != brokerapi.MethodCreateSession {
			t.Errorf("expected method %q, got %q", brokerapi.MethodCreateSession, req.Method)
		}
		return brokerapi.Response{
			OK:   true,
			Body: mustMarshal(t, want),
		}
	})

	client := &Client{SocketPath: sock}
	got, err := client.CreateSession(brokerapi.CreateSessionRequest{
		AgentName: "claude",
		Resources: []string{"github:repo:owner/repo"},
	})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if got.SessionID != want.SessionID {
		t.Errorf("session ID = %q, want %q", got.SessionID, want.SessionID)
	}
}

func TestConnectFailure(t *testing.T) {
	client := &Client{SocketPath: "/nonexistent/broker.sock"}
	_, err := client.CreateSession(brokerapi.CreateSessionRequest{
		AgentName: "test",
		Resources: []string{"github:repo:owner/repo"},
	})
	if err == nil {
		t.Fatal("expected error for nonexistent socket, got nil")
	}
}

func TestRevokeSession(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "broker.sock")

	fakeServer(t, sock, func(req brokerapi.Request) brokerapi.Response {
		if req.Method != brokerapi.MethodRevokeSession {
			t.Errorf("expected method %q, got %q", brokerapi.MethodRevokeSession, req.Method)
		}
		return brokerapi.Response{
			OK:   true,
			Body: mustMarshal(t, brokerapi.RevokeSessionResponse{Revoked: true}),
		}
	})

	client := &Client{SocketPath: sock}
	err := client.RevokeSession(brokerapi.RevokeSessionRequest{
		SessionID:  "sess-1",
		BindSecret: []byte("secret"),
	})
	if err != nil {
		t.Fatalf("RevokeSession: %v", err)
	}
}

func TestSessionStatus(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "broker.sock")

	want := brokerapi.SessionStatusResponse{
		Active:    true,
		AgentName: "claude",
		Resources: []string{"github:repo:owner/repo"},
	}

	fakeServer(t, sock, func(req brokerapi.Request) brokerapi.Response {
		return brokerapi.Response{
			OK:   true,
			Body: mustMarshal(t, want),
		}
	})

	client := &Client{SocketPath: sock}
	got, err := client.SessionStatus(brokerapi.SessionStatusRequest{
		SessionID:  "sess-1",
		BindSecret: []byte("secret"),
	})
	if err != nil {
		t.Fatalf("SessionStatus: %v", err)
	}
	if !got.Active {
		t.Error("expected active=true")
	}

	_ = os.RemoveAll(dir)
}

func TestHealthCheck(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "broker.sock")

	want := brokerapi.HealthCheckResponse{Healthy: true}

	fakeServer(t, sock, func(req brokerapi.Request) brokerapi.Response {
		if req.Method != brokerapi.MethodHealthCheck {
			t.Errorf("expected method %q, got %q", brokerapi.MethodHealthCheck, req.Method)
		}
		return brokerapi.Response{
			OK:   true,
			Body: mustMarshal(t, want),
		}
	})

	client := &Client{SocketPath: sock}
	got, err := client.HealthCheck()
	if err != nil {
		t.Fatalf("HealthCheck: %v", err)
	}
	if !got.Healthy {
		t.Fatal("expected healthy broker response")
	}
}

func TestBrokerErrorWithoutDetails(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "broker.sock")

	fakeServer(t, sock, func(req brokerapi.Request) brokerapi.Response {
		return brokerapi.Response{OK: false}
	})

	client := &Client{SocketPath: sock}
	_, err := client.CreateSession(brokerapi.CreateSessionRequest{
		AgentName: "claude",
		Resources: []string{"github:repo:owner/repo"},
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	berr, ok := err.(*BrokerError)
	if !ok {
		t.Fatalf("expected *BrokerError, got %T", err)
	}
	if berr.Code != "unknown" {
		t.Fatalf("code = %q, want %q", berr.Code, "unknown")
	}
}
