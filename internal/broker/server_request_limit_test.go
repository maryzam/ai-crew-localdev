package broker

import (
	"bytes"
	"encoding/json"
	"net"
	"testing"
	"time"
)

func TestBrokerRejectsOversizedRequest(t *testing.T) {
	_, sockPath, cleanup := testBroker(t)
	defer cleanup()

	conn, err := net.Dial("unix", sockPath)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("set deadline: %v", err)
	}

	filler := bytes.Repeat([]byte("a"), MaxRequestBytes+512)
	body := append([]byte{'"'}, filler...)
	body = append(body, '"')
	req := Request{Method: MethodHealthCheck, Body: json.RawMessage(body)}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		t.Fatalf("encode: %v", err)
	}

	var resp Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.OK {
		t.Fatal("expected oversized request to be rejected")
	}
	if resp.Error.Code != ErrCodeBrokerUnavailable {
		t.Errorf("error code = %q, want %q", resp.Error.Code, ErrCodeBrokerUnavailable)
	}
}

func TestBrokerAcceptsRequestAtBoundary(t *testing.T) {
	_, sockPath, cleanup := testBroker(t)
	defer cleanup()

	body, _ := json.Marshal(HealthCheckRequest{})
	resp := sendRequest(t, sockPath, Request{Method: MethodHealthCheck, Body: body})
	if !resp.OK {
		t.Fatalf("health_check failed: %s", resp.Error.Message)
	}
}
