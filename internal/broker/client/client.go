package client

import (
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/broker/api"
)

const dialTimeout = 5 * time.Second

type Client struct {
	SocketPath string
}

func brokerFailure(resp *api.Response) error {
	if resp != nil && resp.Error != nil {
		return &BrokerError{Code: resp.Error.Code, Message: resp.Error.Message}
	}
	return &BrokerError{Code: "unknown", Message: "request failed without error details"}
}

func (c *Client) call(method string, body interface{}) (*api.Response, error) {
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal request body: %w", err)
	}

	req := api.Request{
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

	var resp api.Response
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	return &resp, nil
}

func (c *Client) CreateSession(req api.CreateSessionRequest) (*api.CreateSessionResponse, error) {
	resp, err := c.call(api.MethodCreateSession, req)
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, brokerFailure(resp)
	}

	var result api.CreateSessionResponse
	if err := json.Unmarshal(resp.Body, &result); err != nil {
		return nil, fmt.Errorf("decode create_session response: %w", err)
	}
	return &result, nil
}

func (c *Client) MintCredential(req api.CredentialRequest) (*api.CredentialResponse, error) {
	resp, err := c.call(api.MethodMintCredential, req)
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, brokerFailure(resp)
	}

	var result api.CredentialResponse
	if err := json.Unmarshal(resp.Body, &result); err != nil {
		return nil, fmt.Errorf("decode mint_credential response: %w", err)
	}
	return &result, nil
}

func (c *Client) PublishTelemetry(req api.PublishTelemetryRequest) (*api.PublishTelemetryResponse, error) {
	resp, err := c.call(api.MethodPublishTelemetry, req)
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, brokerFailure(resp)
	}

	var result api.PublishTelemetryResponse
	if err := json.Unmarshal(resp.Body, &result); err != nil {
		return nil, fmt.Errorf("decode publish_telemetry response: %w", err)
	}
	return &result, nil
}

func (c *Client) RevokeSession(req api.RevokeSessionRequest) error {
	resp, err := c.call(api.MethodRevokeSession, req)
	if err != nil {
		return err
	}
	if !resp.OK {
		return brokerFailure(resp)
	}
	return nil
}

func (c *Client) SessionStatus(req api.SessionStatusRequest) (*api.SessionStatusResponse, error) {
	resp, err := c.call(api.MethodSessionStatus, req)
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, brokerFailure(resp)
	}

	var result api.SessionStatusResponse
	if err := json.Unmarshal(resp.Body, &result); err != nil {
		return nil, fmt.Errorf("decode session_status response: %w", err)
	}
	return &result, nil
}

func (c *Client) HealthCheck() (*api.HealthCheckResponse, error) {
	resp, err := c.call(api.MethodHealthCheck, api.HealthCheckRequest{})
	if err != nil {
		return nil, err
	}
	if !resp.OK {
		return nil, brokerFailure(resp)
	}

	var result api.HealthCheckResponse
	if err := json.Unmarshal(resp.Body, &result); err != nil {
		return nil, fmt.Errorf("decode health_check response: %w", err)
	}
	return &result, nil
}

type BrokerError struct {
	Code    string
	Message string
}

func (e *BrokerError) Error() string {
	return fmt.Sprintf("broker: [%s] %s", e.Code, e.Message)
}
