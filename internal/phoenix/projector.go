package phoenix

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/evidence"
)

const (
	DefaultCollectorEndpoint = "http://127.0.0.1:4319"
	projectionTimeout        = 200 * time.Millisecond
)

type Projector struct {
	endpoint string
	client   *http.Client
}

type RebuildResult struct {
	Projected uint64
	Failed    uint64
}

func NewProjector(endpoint string) (*Projector, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse Phoenix Collector endpoint: %w", err)
	}
	if parsed.Scheme != "http" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "" {
		return nil, errors.New("phoenix collector endpoint must be an HTTP origin")
	}
	host := parsed.Hostname()
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return nil, errors.New("phoenix collector endpoint must be on loopback")
	}
	return &Projector{endpoint: strings.TrimSuffix(endpoint, "/"), client: &http.Client{Timeout: projectionTimeout}}, nil
}

func (p *Projector) Project(ctx context.Context, signal string, payload []byte) (bool, error) {
	if signal != "traces" {
		return false, nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint+"/v1/traces", bytes.NewReader(payload))
	if err != nil {
		return true, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := p.client.Do(request)
	if err != nil {
		return true, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 1024))
		return true, fmt.Errorf("phoenix collector returned %s: %s", response.Status, strings.TrimSpace(string(message)))
	}
	return true, nil
}

func (p *Projector) Rebuild(ctx context.Context, root *evidence.Root) (RebuildResult, error) {
	result := RebuildResult{}
	runsPath := filepath.Join(root.Path(), evidence.RunsDir)
	entries, err := os.ReadDir(runsPath)
	if err != nil {
		return result, fmt.Errorf("list evidence runs: %w", err)
	}
	var rebuildErr error
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return result, errors.Join(rebuildErr, err)
		}
		if !entry.IsDir() {
			continue
		}
		path, pathErr := root.TelemetryPath(entry.Name())
		if pathErr != nil {
			rebuildErr = errors.Join(rebuildErr, pathErr)
			continue
		}
		fileErr := eachTrace(path, func(line []byte) error {
			attempted, projectErr := p.Project(ctx, "traces", line)
			if attempted && projectErr == nil {
				result.Projected++
				return nil
			}
			if attempted {
				result.Failed++
			}
			return projectErr
		})
		if fileErr != nil {
			rebuildErr = errors.Join(rebuildErr, fmt.Errorf("rebuild %s: %w", entry.Name(), fileErr))
		}
	}
	return result, rebuildErr
}

func eachTrace(path string, visit func([]byte) error) error {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 33<<20)
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		var envelope struct {
			ResourceSpans []json.RawMessage `json:"resourceSpans"`
		}
		if err := json.Unmarshal(line, &envelope); err != nil {
			return err
		}
		if len(envelope.ResourceSpans) > 0 {
			if err := visit(line); err != nil {
				return err
			}
		}
	}
	return scanner.Err()
}
