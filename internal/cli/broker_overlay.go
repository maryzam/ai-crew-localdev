package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/maryzam/ai-crew-localdev/internal/config"
)

const (
	containerBrokerDir = "/run/ai-agent"
	containerBinDir    = "/usr/local/ai-agent/bin"
	brokerSocketEnv    = "AI_AGENT_AUTH_SOCK"
	containerPathRef   = "${containerEnv:PATH}"
)

var injectedToolchainBinaries = []string{"ai-agent", "ai-agent-gh", "ai-agent-credential-helper"}

func brokerOverlayArgs(projectRoot string) ([]string, error) {
	overlay, err := newBrokerOverlay(projectRoot)
	if err != nil {
		return nil, err
	}
	return overlay.devcontainerArgs()
}

func projectHasDevcontainer(projectRoot string) bool {
	_, ok := findProjectDevcontainer(projectRoot)
	return ok
}

type projectDevcontainer struct {
	root       string
	configPath string
}

func findProjectDevcontainer(root string) (projectDevcontainer, bool) {
	for _, candidate := range []string{
		filepath.Join(root, ".devcontainer", "devcontainer.json"),
		filepath.Join(root, ".devcontainer.json"),
	} {
		if _, err := os.Stat(candidate); err == nil {
			return projectDevcontainer{root: root, configPath: candidate}, true
		}
	}
	return projectDevcontainer{}, false
}

func (p projectDevcontainer) config() (map[string]any, error) {
	data, err := os.ReadFile(p.configPath)
	if err != nil {
		return nil, fmt.Errorf("read project devcontainer config: %w", err)
	}
	config := map[string]any{}
	if err := json.Unmarshal(stripTrailingCommas(stripComments(data)), &config); err != nil {
		return nil, fmt.Errorf("parse project devcontainer config %s: %w", p.configPath, err)
	}
	return config, nil
}

func (p projectDevcontainer) overlayKey() string {
	digest := sha256.Sum256([]byte(p.root))
	return hex.EncodeToString(digest[:8])
}

type hostToolchain struct {
	binDir   string
	binaries []string
}

func locateHostToolchain() (hostToolchain, error) {
	self, err := osExecutable()
	if err != nil {
		return hostToolchain{}, fmt.Errorf("locate ai-agent binary: %w", err)
	}
	binDir := filepath.Dir(self)
	for _, binary := range injectedToolchainBinaries {
		if _, err := os.Stat(filepath.Join(binDir, binary)); err != nil {
			return hostToolchain{}, fmt.Errorf("ai-agent toolchain incomplete in %s (missing %s); run 'make install'", binDir, binary)
		}
	}
	return hostToolchain{binDir: binDir, binaries: injectedToolchainBinaries}, nil
}

func (t hostToolchain) injections() []injection {
	injections := make([]injection, 0, len(t.binaries))
	for _, binary := range t.binaries {
		injections = append(injections, injection{
			hostPath:      filepath.Join(t.binDir, binary),
			containerPath: path.Join(containerBinDir, binary),
		})
	}
	return injections
}

type injection struct {
	hostPath      string
	containerPath string
}

func (i injection) bindMount() string {
	return fmt.Sprintf("source=%s,target=%s,type=bind,readonly", i.hostPath, i.containerPath)
}

func (i injection) readOnlyVolume() string {
	return i.hostPath + ":" + i.containerPath + ":ro"
}

type brokerOverlay struct {
	project    projectDevcontainer
	toolchain  hostToolchain
	socketDir  string
	socketName string
}

func newBrokerOverlay(projectRoot string) (brokerOverlay, error) {
	project, ok := findProjectDevcontainer(projectRoot)
	if !ok {
		return brokerOverlay{}, fmt.Errorf("project %s has no devcontainer config", projectRoot)
	}
	toolchain, err := locateHostToolchain()
	if err != nil {
		return brokerOverlay{}, err
	}
	return brokerOverlay{
		project:    project,
		toolchain:  toolchain,
		socketDir:  config.RuntimeDir(),
		socketName: filepath.Base(config.DefaultSocketPath()),
	}, nil
}

func (o brokerOverlay) devcontainerArgs() ([]string, error) {
	configPath, err := o.writeConfig()
	if err != nil {
		return nil, err
	}
	return []string{"--override-config", configPath}, nil
}

func (o brokerOverlay) writeConfig() (string, error) {
	merged, err := o.project.config()
	if err != nil {
		return "", err
	}

	if _, composeBacked := merged["dockerComposeFile"]; composeBacked {
		composeOverlay, err := o.writeComposeOverlay(merged)
		if err != nil {
			return "", err
		}
		merged["dockerComposeFile"] = appendComposeFile(merged["dockerComposeFile"], composeOverlay)
	} else {
		merged["mounts"] = append(existingMounts(merged), o.readOnlyMounts()...)
	}
	merged["remoteEnv"] = o.remoteEnv(merged["remoteEnv"])

	encoded, err := json.MarshalIndent(merged, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode broker overlay config: %w", err)
	}
	return o.writeRuntimeFile("devcontainer-broker-overlay", "json", append(encoded, '\n'))
}

func (o brokerOverlay) socketInjection() injection {
	return injection{hostPath: o.socketDir, containerPath: containerBrokerDir}
}

func (o brokerOverlay) readOnlyMounts() []any {
	mounts := []any{o.socketInjection().bindMount()}
	for _, inj := range o.toolchain.injections() {
		mounts = append(mounts, inj.bindMount())
	}
	return mounts
}

func (o brokerOverlay) writeComposeOverlay(projectConfig map[string]any) (string, error) {
	service, ok := projectConfig["service"].(string)
	if !ok || service == "" {
		return "", fmt.Errorf("project devcontainer uses dockerComposeFile but has no service")
	}

	volumes := []string{o.socketInjection().readOnlyVolume()}
	for _, inj := range o.toolchain.injections() {
		volumes = append(volumes, inj.readOnlyVolume())
	}
	return o.writeRuntimeFile("devcontainer-broker-compose-overlay", "yml", composeServiceVolumes(service, volumes))
}

func (o brokerOverlay) remoteEnv(projectEnv any) map[string]any {
	env := cloneStringMap(projectEnv)
	env[brokerSocketEnv] = path.Join(containerBrokerDir, o.socketName)
	env["AI_AGENT_LANGFUSE_HOST"] = "${localEnv:AI_AGENT_LANGFUSE_HOST}"
	env["AI_AGENT_LANGFUSE_PUBLIC_KEY"] = "${localEnv:AI_AGENT_LANGFUSE_PUBLIC_KEY}"
	env["AI_AGENT_LANGFUSE_SECRET_KEY"] = "${localEnv:AI_AGENT_LANGFUSE_SECRET_KEY}"
	env["AI_AGENT_OTLP_TRACES_ENDPOINT"] = "${localEnv:AI_AGENT_OTLP_TRACES_ENDPOINT}"
	env["AI_AGENT_OTLP_HEADERS"] = "${localEnv:AI_AGENT_OTLP_HEADERS}"
	prependToolchainToPath(env)
	return env
}

func (o brokerOverlay) writeRuntimeFile(prefix, extension string, data []byte) (string, error) {
	runtimeDir := config.RuntimeDir()
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		return "", fmt.Errorf("create runtime dir for %s: %w", prefix, err)
	}
	target := filepath.Join(runtimeDir, prefix+"-"+o.project.overlayKey()+"."+extension)
	if err := os.WriteFile(target, data, 0o600); err != nil {
		return "", fmt.Errorf("write %s: %w", prefix, err)
	}
	return target, nil
}

func prependToolchainToPath(env map[string]any) {
	containerPath := containerPathRef
	if projectPath, ok := env["PATH"].(string); ok && projectPath != "" {
		containerPath = projectPath
	}
	env["PATH"] = containerBinDir + ":" + containerPath
}

func cloneStringMap(value any) map[string]any {
	clone := map[string]any{}
	if existing, ok := value.(map[string]any); ok {
		for key, val := range existing {
			clone[key] = val
		}
	}
	return clone
}

func existingMounts(config map[string]any) []any {
	mounts, _ := config["mounts"].([]any)
	return mounts
}

func appendComposeFile(current any, overlayPath string) any {
	switch existing := current.(type) {
	case []any:
		return append(existing, overlayPath)
	case string:
		if existing == "" {
			return overlayPath
		}
		return []any{existing, overlayPath}
	default:
		return overlayPath
	}
}

func composeServiceVolumes(service string, volumes []string) []byte {
	lines := []string{"services:", "  " + quoteYAML(service) + ":", "    volumes:"}
	for _, volume := range volumes {
		lines = append(lines, "      - "+quoteYAML(volume))
	}
	return []byte(strings.Join(lines, "\n") + "\n")
}

func quoteYAML(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func stripComments(data []byte) []byte {
	out := make([]byte, 0, len(data))
	inString := false
	escaped := false
	for i := 0; i < len(data); i++ {
		c := data[i]
		if inString {
			out = append(out, c)
			if escaped {
				escaped = false
				continue
			}
			switch c {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}
		if c == '"' {
			inString = true
			out = append(out, c)
			continue
		}
		if c == '/' && i+1 < len(data) {
			switch data[i+1] {
			case '/':
				for i < len(data) && data[i] != '\n' {
					i++
				}
				if i < len(data) {
					out = append(out, data[i])
				}
				continue
			case '*':
				i += 2
				for i+1 < len(data) && (data[i] != '*' || data[i+1] != '/') {
					if data[i] == '\n' {
						out = append(out, '\n')
					}
					i++
				}
				i++
				continue
			}
		}
		out = append(out, c)
	}
	return out
}

func stripTrailingCommas(data []byte) []byte {
	out := make([]byte, 0, len(data))
	inString := false
	escaped := false
	for i := 0; i < len(data); i++ {
		c := data[i]
		if inString {
			out = append(out, c)
			if escaped {
				escaped = false
				continue
			}
			switch c {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}
		if c == '"' {
			inString = true
			out = append(out, c)
			continue
		}
		if c == ',' {
			j := i + 1
			for j < len(data) && (data[j] == ' ' || data[j] == '\t' || data[j] == '\r' || data[j] == '\n') {
				j++
			}
			if j < len(data) && (data[j] == '}' || data[j] == ']') {
				continue
			}
		}
		out = append(out, c)
	}
	return out
}
