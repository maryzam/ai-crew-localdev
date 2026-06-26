package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

type langfuseSink struct {
	host      string
	publicKey string
	secretKey string
	client    *http.Client
}

func newLangfuseSinkFromEnv() *langfuseSink {
	publicKey := firstEnv("AI_AGENT_LANGFUSE_PUBLIC_KEY", "LANGFUSE_PUBLIC_KEY")
	secretKey := firstEnv("AI_AGENT_LANGFUSE_SECRET_KEY", "LANGFUSE_SECRET_KEY")
	if publicKey == "" || secretKey == "" {
		return nil
	}
	host := firstEnv("AI_AGENT_LANGFUSE_HOST", "LANGFUSE_HOST")
	if host == "" {
		host = defaultLangfuseHost
	}
	return &langfuseSink{
		host:      strings.TrimRight(host, "/"),
		publicKey: publicKey,
		secretKey: secretKey,
		client:    &http.Client{Timeout: 2 * time.Second},
	}
}

func firstEnv(keys ...string) string {
	for _, key := range keys {
		if v := strings.TrimSpace(os.Getenv(key)); v != "" {
			return v
		}
	}
	return ""
}

func (s *langfuseSink) ingest(ev Event, createTrace bool) error {
	items := make([]map[string]any, 0, 2)
	if createTrace {
		items = append(items, map[string]any{
			"id":        ev.RunID + "-trace",
			"type":      "trace-create",
			"timestamp": ev.Timestamp,
			"body": map[string]any{
				"id":        ev.RunID,
				"timestamp": ev.Timestamp,
				"name":      "ai-agent managed run",
				"userId":    ev.AgentName,
				"tags":      []string{"ai-agent", "managed-run"},
				"metadata":  langfuseMetadata(ev),
			},
		})
	}
	items = append(items, map[string]any{
		"id":        ev.RunID + "-" + ev.EventType + "-" + ev.Timestamp.Format("20060102T150405.000000000Z07:00"),
		"type":      "event-create",
		"timestamp": ev.Timestamp,
		"body": map[string]any{
			"id":        ev.RunID + "-" + sha256Hex(ev.EventType + ev.Timestamp.String())[:16],
			"traceId":   ev.RunID,
			"name":      ev.EventType,
			"startTime": ev.Timestamp,
			"metadata":  langfuseMetadata(ev),
		},
	})

	payload := map[string]any{
		"batch": items,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("langfuse: marshal payload: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.host+langfuseIngestPath, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("langfuse: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(s.publicKey, s.secretKey)

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("langfuse: ingest: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("langfuse: ingest status %d", resp.StatusCode)
	}
	return nil
}

func langfuseMetadata(ev Event) map[string]any {
	metadata := map[string]any{
		"run_id":      ev.RunID,
		"session_id":  ev.SessionID,
		"event_type":  ev.EventType,
		"agent_name":  ev.AgentName,
		"repo":        ev.Repo,
		"model":       ev.Model,
		"attempt":     ev.Attempt,
		"outcome":     ev.Outcome,
		"duration_ms": ev.DurationMS,
	}
	if ev.ExitCode != nil {
		metadata["exit_code"] = *ev.ExitCode
	}
	if ev.Usage != nil {
		metadata["usage"] = ev.Usage
	}
	for k, v := range ev.Metadata {
		metadata[k] = v
	}
	return metadata
}
