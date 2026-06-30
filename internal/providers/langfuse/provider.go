package langfuse

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/maryzam/ai-crew-localdev/internal/brokerapi"
	"github.com/maryzam/ai-crew-localdev/internal/brokerport"
	langfusecontract "github.com/maryzam/ai-crew-localdev/internal/providers/langfuse/contract"
	"golang.org/x/sys/unix"
)

const (
	credentialType = langfusecontract.CredentialType
	uriProvider    = "langfuse"
	uriKind        = "project"
	maxFileBytes   = 64 * 1024
)

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

func (p *Provider) Type() string        { return credentialType }
func (p *Provider) URIProvider() string { return uriProvider }

func (p *Provider) ValidateResource(uri brokerapi.ResourceURI) error {
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

func (p *Provider) PrepareMint(params json.RawMessage, config any) (string, error) {
	if len(params) != 0 && string(params) != "null" && string(params) != "{}" {
		return "", fmt.Errorf("langfuse provider: mint params are not supported")
	}
	cfg, err := assertConfig(config)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(cfg.CredentialsFile)
	if err != nil {
		return "", fmt.Errorf("langfuse provider: inspect credentials file: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", fmt.Errorf("langfuse provider: credentials file must be a regular file, not a symlink")
	}
	identity := strings.Join([]string{
		cfg.CredentialsFile,
		cfg.Endpoint,
		cfg.Project,
		strconv.FormatInt(info.Size(), 10),
		strconv.FormatInt(info.ModTime().UnixNano(), 10),
	}, "|")
	sum := sha256.Sum256([]byte(identity))
	return hex.EncodeToString(sum[:]), nil
}

func (p *Provider) Mint(_ context.Context, req brokerport.ProviderMintRequest) (brokerport.ProviderMintResult, error) {
	cfg, err := assertConfig(req.Config)
	if err != nil {
		return brokerport.ProviderMintResult{}, err
	}
	if req.Resource.Provider != uriProvider || req.Resource.Kind != uriKind || req.Resource.Identifier != cfg.Project {
		return brokerport.ProviderMintResult{}, fmt.Errorf("langfuse provider: resource does not match configured project")
	}
	values, err := readCredentialsFile(cfg.CredentialsFile)
	if err != nil {
		return brokerport.ProviderMintResult{}, err
	}
	publicKey := values["LANGFUSE_INIT_PROJECT_PUBLIC_KEY"]
	secretKey := values["LANGFUSE_INIT_PROJECT_SECRET_KEY"]
	if publicKey == "" || secretKey == "" {
		return brokerport.ProviderMintResult{}, fmt.Errorf("langfuse provider: credentials file is missing project keys")
	}
	payload, err := json.Marshal(langfusecontract.Credential{
		Endpoint:  cfg.Endpoint,
		PublicKey: publicKey,
		SecretKey: secretKey,
	})
	if err != nil {
		return brokerport.ProviderMintResult{}, fmt.Errorf("langfuse provider: encode credential: %w", err)
	}
	return brokerport.ProviderMintResult{Credential: payload, ExpiresAt: time.Now().Add(5 * time.Minute)}, nil
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
