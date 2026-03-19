// Package brokerclient provides a client for the ai-agent broker Unix socket API.
//
// The broker uses a one-request-per-connection model: the client connects,
// sends a JSON request, reads a JSON response, and disconnects.
package brokerclient

import (
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/broker"
)

const dialTimeout = 5 * time.Second

// Client communicates with the broker daemon over a Unix socket.
type Client struct {
	SocketPath string
}

func brokerFailure(resp *broker.Response) error {
	if resp != nil && resp.Error != nil {
		return &BrokerError{Code: resp.Error.Code, Message: resp.Error.Message}
	}
	return &BrokerError{Code: "unknown", Message: "request failed without error details"}
}

// call sends a single request to the broker and returns the decoded response.
func (c *Client) call(method string, body interface{}) (*broker.Response, error) {
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request body: %w", err)
	}

	req := broker.Request{
		Method: method,
		Body:   bodyJSON,
	}

	conn, err := net.DialTimeout("unix", c.SocketPath, dialTimeout)
	if err != nil {
		return nil, fmt.Errorf("connect to broker at %s: %w", c.SocketPath, err)
	}
	defer func() { _ = conn.Close() }()

	if err := conn.SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
		return nil, fmt.Errorf("set deadline: %w", err)
	}

	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return nil, fmt.Errorf("send request: %w", err)
	}

	var resp broker.Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	return &resp, nil
}

// CreateSession asks the broker to create a new session.
func (c *Client) CreateSession(req broker.CreateSessionRequest) (*broker.CreateSessionResponse, error) {
	resp, err := c.call(broker.MethodCreateSession, req)
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, brokerFailure(resp)
	}

	var result broker.CreateSessionResponse
	if err := json.Unmarshal(resp.Body, &result); err != nil {
		return nil, fmt.Errorf("decode create_session response: %w", err)
	}
	return &result, nil
}

// MintToken asks the broker to mint a GitHub installation token.
func (c *Client) MintToken(req broker.TokenRequest) (*broker.TokenResponse, error) {
	resp, err := c.call(broker.MethodMintToken, req)
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, brokerFailure(resp)
	}

	var result broker.TokenResponse
	if err := json.Unmarshal(resp.Body, &result); err != nil {
		return nil, fmt.Errorf("decode mint_token response: %w", err)
	}
	return &result, nil
}

// RevokeSession asks the broker to revoke an existing session.
func (c *Client) RevokeSession(req broker.RevokeSessionRequest) error {
	resp, err := c.call(broker.MethodRevokeSession, req)
	if err != nil {
		return err
	}
	if !resp.OK {
		return brokerFailure(resp)
	}
	return nil
}

// SessionStatus queries the broker for a session's current state.
func (c *Client) SessionStatus(req broker.SessionStatusRequest) (*broker.SessionStatusResponse, error) {
	resp, err := c.call(broker.MethodSessionStatus, req)
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, brokerFailure(resp)
	}

	var result broker.SessionStatusResponse
	if err := json.Unmarshal(resp.Body, &result); err != nil {
		return nil, fmt.Errorf("decode session_status response: %w", err)
	}
	return &result, nil
}

// HealthCheck asks the broker to confirm the socket is live and serving
// requests.
func (c *Client) HealthCheck() (*broker.HealthCheckResponse, error) {
	resp, err := c.call(broker.MethodHealthCheck, broker.HealthCheckRequest{})
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, brokerFailure(resp)
	}

	var result broker.HealthCheckResponse
	if err := json.Unmarshal(resp.Body, &result); err != nil {
		return nil, fmt.Errorf("decode health_check response: %w", err)
	}
	return &result, nil
}

// BrokerError is a structured error from the broker with a machine-readable code.
type BrokerError struct {
	Code    string
	Message string
}

func (e *BrokerError) Error() string {
	return fmt.Sprintf("broker: [%s] %s", e.Code, e.Message)
}
