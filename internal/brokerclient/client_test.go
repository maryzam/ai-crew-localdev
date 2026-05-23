package brokerclient

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/maryzam/ai-crew-localdev/internal/broker"
)

// fakeServer accepts one connection, reads a request, and writes a canned response.
func fakeServer(t *testing.T, socketPath string, handler func(broker.Request) broker.Response) {
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

		var req broker.Request
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

	want := broker.CreateSessionResponse{
		SessionID:  "test-session-123",
		BindSecret: []byte("secret-bytes"),
	}

	fakeServer(t, sock, func(req broker.Request) broker.Response {
		if req.Method != broker.MethodCreateSession {
			t.Errorf("expected method %q, got %q", broker.MethodCreateSession, req.Method)
		}
		return broker.Response{
			OK:   true,
			Body: mustMarshal(t, want),
		}
	})

	client := &Client{SocketPath: sock}
	got, err := client.CreateSession(broker.CreateSessionRequest{
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
	_, err := client.CreateSession(broker.CreateSessionRequest{
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

	fakeServer(t, sock, func(req broker.Request) broker.Response {
		if req.Method != broker.MethodRevokeSession {
			t.Errorf("expected method %q, got %q", broker.MethodRevokeSession, req.Method)
		}
		return broker.Response{
			OK:   true,
			Body: mustMarshal(t, broker.RevokeSessionResponse{Revoked: true}),
		}
	})

	client := &Client{SocketPath: sock}
	err := client.RevokeSession(broker.RevokeSessionRequest{
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

	want := broker.SessionStatusResponse{
		Active:    true,
		AgentName: "claude",
		Resources: []string{"github:repo:owner/repo"},
	}

	fakeServer(t, sock, func(req broker.Request) broker.Response {
		return broker.Response{
			OK:   true,
			Body: mustMarshal(t, want),
		}
	})

	client := &Client{SocketPath: sock}
	got, err := client.SessionStatus(broker.SessionStatusRequest{
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

	want := broker.HealthCheckResponse{Healthy: true}

	fakeServer(t, sock, func(req broker.Request) broker.Response {
		if req.Method != broker.MethodHealthCheck {
			t.Errorf("expected method %q, got %q", broker.MethodHealthCheck, req.Method)
		}
		return broker.Response{
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

	fakeServer(t, sock, func(req broker.Request) broker.Response {
		return broker.Response{OK: false}
	})

	client := &Client{SocketPath: sock}
	_, err := client.CreateSession(broker.CreateSessionRequest{
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
