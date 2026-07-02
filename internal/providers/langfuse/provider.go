package langfuse

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/broker/api"
	"github.com/maryzam/ai-crew-localdev/internal/broker/port"
	"github.com/maryzam/ai-crew-localdev/internal/platform/telemetry"
	"golang.org/x/sys/unix"
)

const (
	uriProvider   = "langfuse"
	uriKind       = "project"
	maxFileBytes  = 64 * 1024
	exportTimeout = 2 * time.Second
)

var newHTTPClient = func() *http.Client {
	return &http.Client{
		Timeout: exportTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

type Provider struct{}

type Config struct {
	CredentialsFile string
	Endpoint        string
	Project         string
}

type rawConfig struct {
	CredentialsFile string `json:"credentials_file"`
	Endpoint        string `json:"endpoint"`
	Project         string `json:"project"`
}

func New() *Provider { return &Provider{} }

func (p *Provider) URIProvider() string { return uriProvider }

func (p *Provider) ValidateResource(uri api.ResourceURI) error {
	if uri.Provider != uriProvider || uri.Kind != uriKind {
		return fmt.Errorf("langfuse provider: expected %s:%s resource", uriProvider, uriKind)
	}
	if strings.TrimSpace(uri.Identifier) == "" || len(uri.Identifier) > 128 {
		return fmt.Errorf("langfuse provider: invalid project identifier")
	}
	return nil
}

func (p *Provider) ParseConfig(agent string, section json.RawMessage) (any, error) {
	var raw rawConfig
	if err := json.Unmarshal(section, &raw); err != nil {
		return nil, fmt.Errorf("agent %q: providers.langfuse: %w", agent, err)
	}
	if !filepath.IsAbs(raw.CredentialsFile) {
		return nil, fmt.Errorf("agent %q: providers.langfuse.credentials_file must be absolute", agent)
	}
	if err := validateEndpoint(raw.Endpoint); err != nil {
		return nil, fmt.Errorf("agent %q: providers.langfuse.endpoint: %w", agent, err)
	}
	if strings.TrimSpace(raw.Project) == "" {
		return nil, fmt.Errorf("agent %q: providers.langfuse.project must not be empty", agent)
	}
	return Config{
		CredentialsFile: filepath.Clean(raw.CredentialsFile),
		Endpoint:        strings.TrimRight(raw.Endpoint, "/"),
		Project:         raw.Project,
	}, nil
}

func (p *Provider) PublishTelemetry(ctx context.Context, req port.ProviderTelemetryRequest) error {
	cfg, err := assertConfig(req.Config)
	if err != nil {
		return err
	}
	if req.Resource.Provider != uriProvider || req.Resource.Kind != uriKind || req.Resource.Identifier != cfg.Project {
		return fmt.Errorf("langfuse provider: resource does not match configured project")
	}
	if err := telemetry.ValidateOTLPExportPayload(req.Payload); err != nil {
		return fmt.Errorf("langfuse provider: reject telemetry payload: %w", err)
	}
	values, err := readCredentialsFile(cfg.CredentialsFile)
	if err != nil {
		return err
	}
	publicKey := values["LANGFUSE_INIT_PROJECT_PUBLIC_KEY"]
	secretKey := values["LANGFUSE_INIT_PROJECT_SECRET_KEY"]
	if publicKey == "" || secretKey == "" {
		return fmt.Errorf("langfuse provider: credentials file is missing project keys")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, traceEndpoint(cfg.Endpoint), bytes.NewReader(req.Payload))
	if err != nil {
		return fmt.Errorf("langfuse provider: build OTLP request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("x-langfuse-ingestion-version", "4")
	request.SetBasicAuth(publicKey, secretKey)
	response, err := newHTTPClient().Do(request)
	if err != nil {
		return fmt.Errorf("langfuse provider: export OTLP: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("langfuse provider: export OTLP status %d", response.StatusCode)
	}
	return nil
}

func traceEndpoint(endpoint string) string {
	parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(endpoint), "/"))
	if err == nil && parsed.Hostname() == "host.containers.internal" {
		if port := parsed.Port(); port != "" {
			parsed.Host = net.JoinHostPort("127.0.0.1", port)
		} else {
			parsed.Host = "127.0.0.1"
		}
		endpoint = parsed.String()
	}
	if strings.HasSuffix(endpoint, "/v1/traces") {
		return endpoint
	}
	return endpoint + "/v1/traces"
}

func assertConfig(value any) (Config, error) {
	cfg, ok := value.(Config)
	if !ok {
		return Config{}, fmt.Errorf("langfuse provider: unexpected config type %T", value)
	}
	return cfg, nil
}

func validateEndpoint(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("scheme must be http or https")
	}
	if parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("must contain only scheme, host, and optional path")
	}
	return nil
}

func readCredentialsFile(path string) (map[string]string, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("langfuse provider: open credentials file: %w", err)
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("langfuse provider: open credentials file")
	}
	defer func() { _ = file.Close() }()

	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return nil, fmt.Errorf("langfuse provider: inspect credentials file: %w", err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Mode&0o077 != 0 {
		return nil, fmt.Errorf("langfuse provider: credentials file must be owner-only and regular")
	}
	if stat.Uid != uint32(os.Getuid()) {
		return nil, fmt.Errorf("langfuse provider: credentials file owner does not match broker user")
	}

	limited := io.LimitReader(file, maxFileBytes+1)
	scanner := bufio.NewScanner(limited)
	values := make(map[string]string)
	read := 0
	for scanner.Scan() {
		read += len(scanner.Bytes()) + 1
		if read > maxFileBytes {
			return nil, fmt.Errorf("langfuse provider: credentials file exceeds %d bytes", maxFileBytes)
		}
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if ok {
			values[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(value), `"'`)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("langfuse provider: read credentials file: %w", err)
	}
	return values, nil
}
