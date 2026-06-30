package broker

import (
	"bytes"
	"encoding/json"
	"github.com/maryzam/ai-crew-localdev/internal/brokerapi"
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
	req := brokerapi.Request{Method: brokerapi.MethodHealthCheck, Body: json.RawMessage(body)}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		t.Fatalf("encode: %v", err)
	}

	var resp brokerapi.Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.OK {
		t.Fatal("expected oversized request to be rejected")
	}
	if resp.Error.Code != brokerapi.ErrCodeBrokerUnavailable {
		t.Errorf("error code = %q, want %q", resp.Error.Code, brokerapi.ErrCodeBrokerUnavailable)
	}
}

func TestBrokerAcceptsRequestAtBoundary(t *testing.T) {
	_, sockPath, cleanup := testBroker(t)
	defer cleanup()

	body, _ := json.Marshal(brokerapi.HealthCheckRequest{})
	resp := sendRequest(t, sockPath, brokerapi.Request{Method: brokerapi.MethodHealthCheck, Body: body})
	if !resp.OK {
		t.Fatalf("health_check failed: %s", resp.Error.Message)
	}
}
