package uphost

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/maryzam/ai-crew-localdev/internal/configmodel/governance"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/identity"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/policy"
	"github.com/maryzam/ai-crew-localdev/internal/platform/embedasset"
	"github.com/maryzam/ai-crew-localdev/internal/platform/paths"
	"github.com/maryzam/ai-crew-localdev/internal/platform/securefile"
	"github.com/maryzam/ai-crew-localdev/internal/runtime/assetsource"
	"github.com/maryzam/ai-crew-localdev/internal/runtime/uphost/assets"
)

const maxLangfuseEnvBytes = 64 * 1024

func StartObservability(ctx context.Context, streams Streams, progress ProgressFunc, validate func(*policy.PolicyFile, *identity.IdentitiesFile) error) error {
	composePath, err := findLangfuseCompose()
	if err != nil {
		return err
	}
	composeDir := filepath.Dir(composePath)
	envPath := filepath.Join(composeDir, ".env")
	examplePath := filepath.Join(composeDir, ".env.example")
	if _, err := os.Stat(envPath); os.IsNotExist(err) {
		if _, err := os.Stat(examplePath); err != nil {
			return fmt.Errorf("langfuse .env.example not found at %s", examplePath)
		}
		data, err := os.ReadFile(examplePath)
		if err != nil {
			return fmt.Errorf("read .env.example: %w", err)
		}
		if err := securefile.WriteOwnerOnly(envPath, data); err != nil {
			return fmt.Errorf("write .env: %w", err)
		}
		report(progress, Progress{Kind: LangfuseEnvironment})
	}
	client, err := loadLangfuseClientEnvironment(envPath)
	if err != nil {
		return err
	}
	if err := configureLangfusePolicy(envPath, client, governance.DefaultPaths(), validate); err != nil {
		return err
	}
	if err := os.Setenv(paths.EnvObservabilityResource, client.Resource); err != nil {
		return err
	}
	report(progress, Progress{Kind: LangfuseStarting})
	if err := runCommand(ctx, "docker", []string{"compose", "-f", composePath, "up", "-d", "--wait"}, Streams{Out: streams.Out, Err: streams.Err}); err != nil {
		return fmt.Errorf("docker compose up: %w", err)
	}
	report(progress, Progress{Kind: LangfuseReady})
	return nil
}

type langfuseClientConfig struct {
	Project  string
	Endpoint string
	Resource string
}

func loadLangfuseClientEnvironment(path string) (langfuseClientConfig, error) {
	data, err := securefile.ReadOwnerOnly(path, maxLangfuseEnvBytes)
	if err != nil {
		return langfuseClientConfig{}, fmt.Errorf("open langfuse environment: %w", err)
	}
	values := make(map[string]string)
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
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
		return langfuseClientConfig{}, fmt.Errorf("read langfuse environment: %w", err)
	}
	if values["LANGFUSE_INIT_PROJECT_PUBLIC_KEY"] == "" || values["LANGFUSE_INIT_PROJECT_SECRET_KEY"] == "" {
		return langfuseClientConfig{}, fmt.Errorf("langfuse .env must define LANGFUSE_INIT_PROJECT_PUBLIC_KEY and LANGFUSE_INIT_PROJECT_SECRET_KEY")
	}
	project := strings.TrimSpace(values["LANGFUSE_INIT_PROJECT_ID"])
	if project == "" {
		return langfuseClientConfig{}, fmt.Errorf("langfuse .env must define LANGFUSE_INIT_PROJECT_ID")
	}
	endpoint := strings.TrimSpace(values[paths.EnvLangfuseOTLPEndpoint])
	if endpoint == "" {
		endpoint = "http://host.containers.internal:3000/api/public/otel"
	}
	return langfuseClientConfig{Project: project, Endpoint: endpoint, Resource: "langfuse:project:" + project}, nil
}

func configureLangfusePolicy(credentialsFile string, client langfuseClientConfig, governancePaths governance.Paths, validator func(*policy.PolicyFile, *identity.IdentitiesFile) error) error {
	if _, err := securefile.ValidateOwnerOnly(credentialsFile, maxLangfuseEnvBytes); err != nil {
		return fmt.Errorf("inspect langfuse credentials: %w", err)
	}
	governanceStore := governance.FileStore{}
	snapshot, err := governanceStore.Load(governancePaths)
	if err != nil {
		return fmt.Errorf("load governance configuration: %w", err)
	}
	if snapshot.IdentitiesError != nil {
		return fmt.Errorf("load governance identities: %w", snapshot.IdentitiesError)
	}
	if snapshot.PolicyError != nil {
		return fmt.Errorf("load governance policy: %w", snapshot.PolicyError)
	}
	idents, pol := snapshot.Identities, snapshot.Policy
	section, err := json.Marshal(map[string]string{"credentials_file": credentialsFile, "endpoint": client.Endpoint, "project": client.Project})
	if err != nil {
		return fmt.Errorf("encode langfuse policy: %w", err)
	}
	resource := "langfuse:project:" + client.Project
	for name, agent := range pol.Agents {
		if !contains(agent.Resources, resource) {
			agent.Resources = append(agent.Resources, resource)
		}
		if agent.Providers == nil {
			agent.Providers = make(map[string]json.RawMessage)
		}
		agent.Providers["langfuse"] = section
		pol.Agents[name] = agent
	}
	if err := validator(pol, idents); err != nil {
		return fmt.Errorf("validate langfuse policy: %w", err)
	}
	if err := governanceStore.Publish(governancePaths, idents, pol); err != nil {
		return fmt.Errorf("publish langfuse policy: %w", err)
	}
	reloadBroker()
	return nil
}

func findLangfuseCompose() (string, error) {
	if dir, ok := assetsource.TrustedCheckoutDir(); ok {
		candidate := filepath.Join(dir, "contrib", "langfuse", "docker-compose.yml")
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			return candidate, nil
		}
	}
	return prepareLangfuseCompose(paths.DataDir())
}

func prepareLangfuseCompose(dataDir string) (string, error) {
	langfuse, err := assets.Langfuse()
	if err != nil {
		return "", fmt.Errorf("load langfuse assets: %w", err)
	}
	composeDir := filepath.Join(dataDir, assets.LangfuseDir)
	if err := embedasset.Materialize(langfuse, composeDir, func(string) fs.FileMode { return 0o600 }); err != nil {
		return "", err
	}
	return filepath.Join(composeDir, "docker-compose.yml"), nil
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func reloadBroker() {
	data, err := os.ReadFile(filepath.Join(paths.RuntimeDir(), "broker.pid"))
	if err != nil {
		return
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err == nil && pid > 1 {
		_ = syscall.Kill(pid, syscall.SIGHUP)
	}
}

func report(sink ProgressFunc, progress Progress) {
	if sink != nil {
		sink(progress)
	}
}
