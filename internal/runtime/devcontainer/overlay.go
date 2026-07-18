package devcontainer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/maryzam/ai-crew-localdev/internal/configmodel/manifest"
	"github.com/maryzam/ai-crew-localdev/internal/platform/paths"
	"github.com/maryzam/ai-crew-localdev/internal/platform/securefile"
	"github.com/maryzam/ai-crew-localdev/internal/providers/capabilities"
)

const (
	containerBrokerDir = "/run/ai-agent"
	ContainerBinDir    = "/usr/local/ai-agent/bin"
	containerPathRef   = "${containerEnv:PATH}"
)

var toolchainBinaries = []string{"ai-agent", "ai-agent-gh", "ai-agent-credential-helper"}

type OverlayBuilder struct {
	Executable func() (string, error)
	Binaries   []string
}

func NewOverlayBuilder(executable func() (string, error)) OverlayBuilder {
	return OverlayBuilder{Executable: executable, Binaries: interposedNames()}
}

func interposedNames() []string {
	names := append([]string(nil), toolchainBinaries...)
	seen := make(map[string]struct{}, len(names))
	for _, name := range names {
		seen[name] = struct{}{}
	}
	for _, command := range capabilities.Commands() {
		if _, dup := seen[command]; dup {
			continue
		}
		seen[command] = struct{}{}
		names = append(names, command)
	}
	return names
}

func (b OverlayBuilder) Args(projectRoot string) ([]string, error) {
	overlay, err := newBrokerOverlay(projectRoot, b)
	if err != nil {
		return nil, err
	}
	return overlay.devcontainerArgs()
}

func ProjectHasConfig(projectRoot string) bool {
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
	hostBinary string
	binaries   []string
}

func locateHostToolchain(builder OverlayBuilder) (hostToolchain, error) {
	if builder.Executable == nil {
		return hostToolchain{}, fmt.Errorf("locate ai-agent binary: executable resolver is not configured")
	}
	self, err := builder.Executable()
	if err != nil {
		return hostToolchain{}, fmt.Errorf("locate ai-agent binary: %w", err)
	}
	hostBinary := filepath.Join(filepath.Dir(self), "ai-agent")
	if _, err := os.Stat(hostBinary); err != nil {
		return hostToolchain{}, fmt.Errorf("ai-agent binary not found at %s; run 'make install'", hostBinary)
	}
	binaries := append([]string(nil), builder.Binaries...)
	return hostToolchain{hostBinary: hostBinary, binaries: binaries}, nil
}

func (t hostToolchain) injections() []injection {
	injections := make([]injection, 0, len(t.binaries))
	for _, binary := range t.binaries {
		injections = append(injections, injection{
			hostPath:      t.hostBinary,
			containerPath: path.Join(ContainerBinDir, binary),
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
	manifest   *manifest.File
}

func newBrokerOverlay(projectRoot string, builder OverlayBuilder) (brokerOverlay, error) {
	project, ok := findProjectDevcontainer(projectRoot)
	if !ok {
		return brokerOverlay{}, fmt.Errorf("project %s has no devcontainer config", projectRoot)
	}
	toolchain, err := locateHostToolchain(builder)
	if err != nil {
		return brokerOverlay{}, err
	}
	socketPath, err := paths.BrokerListenSocketPath()
	if err != nil {
		return brokerOverlay{}, err
	}
	projectManifest, err := loadProjectManifest(projectRoot)
	if err != nil {
		return brokerOverlay{}, err
	}
	if err := enforceProjectDevcontainerMode(projectManifest); err != nil {
		return brokerOverlay{}, err
	}
	if err := enforceProjectApprovals(projectManifest); err != nil {
		return brokerOverlay{}, err
	}
	return brokerOverlay{
		project:    project,
		toolchain:  toolchain,
		socketDir:  filepath.Dir(socketPath),
		socketName: filepath.Base(socketPath),
		manifest:   projectManifest,
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

	configuredWorkspace := configuredWorkspaceFolder(merged)
	if err := validateWorkspaceCacheTargets(o.manifest, configuredWorkspace); err != nil {
		return "", fmt.Errorf("invalid project manifest %s: %w", manifest.PathIn(o.project.root), err)
	}

	manifestServices := o.manifest.ServiceNames()
	manifestPorts := o.manifest.PortNumbers()
	if _, composeBacked := merged["dockerComposeFile"]; composeBacked {
		composeOverlay, err := o.writeComposeOverlay(merged)
		if err != nil {
			return "", err
		}
		merged["dockerComposeFile"] = appendComposeFile(merged["dockerComposeFile"], composeOverlay)
		if runServices := appendRunServices(merged["runServices"], manifestServices); runServices != nil {
			merged["runServices"] = runServices
		}
	} else {
		if len(manifestServices) > 0 {
			return "", fmt.Errorf("project manifest declares services but %s is not compose-backed", o.project.configPath)
		}
		mounts := append(existingMounts(merged), o.readOnlyMounts()...)
		mounts = append(mounts, o.cacheMounts()...)
		merged["mounts"] = mounts
	}
	if forwardPorts := appendForwardPorts(merged["forwardPorts"], manifestPorts); forwardPorts != nil {
		merged["forwardPorts"] = forwardPorts
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

func (o brokerOverlay) cacheMounts() []any {
	if o.manifest == nil {
		return nil
	}
	mounts := make([]any, 0, len(o.manifest.Caches))
	for _, cache := range o.manifest.Caches {
		mount := fmt.Sprintf("source=%s,target=%s,type=volume", o.cacheVolumeName(cache.Name), cache.Target)
		if cache.ReadOnly {
			mount += ",readonly"
		}
		mounts = append(mounts, mount)
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
	manifestCaches := o.manifestCaches()
	for _, cache := range manifestCaches {
		volumes = append(volumes, cache.composeVolume)
	}
	return o.writeRuntimeFile("devcontainer-broker-compose-overlay", "yml", composeServiceVolumes(service, volumes, manifestCaches))
}

func (o brokerOverlay) remoteEnv(projectEnv any) map[string]any {
	env := cloneStringMap(projectEnv)
	env[paths.EnvAuthSock] = path.Join(containerBrokerDir, o.socketName)
	env[paths.EnvContainer] = "1"
	env[paths.EnvObservabilityResource] = o.observabilityResourceEnv()
	prependToolchainToPath(env)
	return env
}

func (o brokerOverlay) observabilityResourceEnv() string {
	if o.manifest != nil {
		for _, resource := range o.manifest.Resources {
			if sink, err := capabilities.ObservabilitySink(resource.URI); err == nil {
				return sink.URI
			}
		}
	}
	return "${localEnv:" + paths.EnvObservabilityResource + "}"
}

func (o brokerOverlay) writeRuntimeFile(prefix, extension string, data []byte) (string, error) {
	runtimeDir := paths.RuntimeDir()
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		return "", fmt.Errorf("create runtime dir for %s: %w", prefix, err)
	}
	target := filepath.Join(runtimeDir, prefix+"-"+o.project.overlayKey()+"."+extension)
	if err := securefile.WriteOwnerOnly(target, data); err != nil {
		return "", fmt.Errorf("write %s: %w", prefix, err)
	}
	return target, nil
}

func prependToolchainToPath(env map[string]any) {
	containerPath := containerPathRef
	if projectPath, ok := env["PATH"].(string); ok && projectPath != "" {
		containerPath = projectPath
	}
	env["PATH"] = ContainerBinDir + ":" + containerPath
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

type composeCacheVolume struct {
	name          string
	composeVolume string
}

func (o brokerOverlay) manifestCaches() []composeCacheVolume {
	if o.manifest == nil {
		return nil
	}
	caches := make([]composeCacheVolume, 0, len(o.manifest.Caches))
	for _, cache := range o.manifest.Caches {
		suffix := ""
		if cache.ReadOnly {
			suffix = ":ro"
		}
		name := o.cacheVolumeName(cache.Name)
		caches = append(caches, composeCacheVolume{name: name, composeVolume: name + ":" + cache.Target + suffix})
	}
	return caches
}

func (o brokerOverlay) cacheVolumeName(name string) string {
	return "ai-agent-cache-" + o.project.overlayKey() + "-" + sanitizeVolumeName(name)
}

func sanitizeVolumeName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	if b.Len() == 0 {
		return "cache"
	}
	return b.String()
}

func composeServiceVolumes(service string, volumes []string, namedVolumes []composeCacheVolume) []byte {
	lines := []string{"services:", "  " + quoteYAML(service) + ":", "    volumes:"}
	for _, volume := range volumes {
		lines = append(lines, "      - "+quoteYAML(volume))
	}
	if len(namedVolumes) > 0 {
		lines = append(lines, "volumes:")
		for _, volume := range namedVolumes {
			lines = append(lines, "  "+quoteYAML(volume.name)+":")
		}
	}
	return []byte(strings.Join(lines, "\n") + "\n")
}

func appendRunServices(current any, services []string) any {
	if len(services) == 0 {
		return current
	}
	seen := map[string]struct{}{}
	result := make([]any, 0)
	switch existing := current.(type) {
	case []any:
		for _, service := range existing {
			if name, ok := service.(string); ok && name != "" {
				if _, dup := seen[name]; !dup {
					result = append(result, name)
					seen[name] = struct{}{}
				}
			}
		}
	case string:
		if existing != "" {
			result = append(result, existing)
			seen[existing] = struct{}{}
		}
	}
	for _, service := range services {
		if _, dup := seen[service]; dup {
			continue
		}
		result = append(result, service)
		seen[service] = struct{}{}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func appendForwardPorts(current any, ports []int) any {
	if len(ports) == 0 {
		return current
	}
	seen := map[int]struct{}{}
	result := make([]any, 0)
	if existing, ok := current.([]any); ok {
		for _, port := range existing {
			result = append(result, port)
			switch value := port.(type) {
			case float64:
				seen[int(value)] = struct{}{}
			case int:
				seen[value] = struct{}{}
			}
		}
	}
	for _, port := range ports {
		if _, dup := seen[port]; dup {
			continue
		}
		result = append(result, port)
		seen[port] = struct{}{}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func loadProjectManifest(projectRoot string) (*manifest.File, error) {
	manifestPath, found, err := manifest.Find(projectRoot)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}
	file, err := manifest.Load(manifestPath)
	if err != nil {
		return nil, err
	}
	result := manifest.Validate(file)
	if result.Errors.HasErrors() {
		return nil, fmt.Errorf("invalid project manifest %s: %s", manifestPath, result.Errors.Error())
	}
	if err := validateOverlayManifest(file); err != nil {
		return nil, fmt.Errorf("invalid project manifest %s: %w", manifestPath, err)
	}
	return file, nil
}

func validateOverlayManifest(file *manifest.File) error {
	if file == nil {
		return nil
	}
	seenCacheVolumes := make(map[string]string, len(file.Caches))
	for _, cache := range file.Caches {
		if overlapsContainerPath(cache.Target, containerBrokerDir) || overlapsContainerPath(cache.Target, ContainerBinDir) {
			return fmt.Errorf("cache %q targets reserved ai-agent path %s", cache.Name, cache.Target)
		}
		volumeName := sanitizeVolumeName(cache.Name)
		if previous, dup := seenCacheVolumes[volumeName]; dup {
			return fmt.Errorf("cache %q and cache %q produce the same volume name %q", previous, cache.Name, volumeName)
		}
		seenCacheVolumes[volumeName] = cache.Name
	}
	return nil
}

func validateWorkspaceCacheTargets(file *manifest.File, workspaceFolder string) error {
	if file == nil || workspaceFolder == "" {
		return nil
	}
	for _, cache := range file.Caches {
		if shadowsContainerPath(cache.Target, workspaceFolder) {
			return fmt.Errorf("cache %q target %s shadows workspace folder %s", cache.Name, cache.Target, workspaceFolder)
		}
	}
	return nil
}

func overlapsContainerPath(target string, reserved string) bool {
	cleanTarget := path.Clean(target)
	cleanReserved := path.Clean(reserved)
	if cleanTarget == "/" || cleanTarget == cleanReserved {
		return true
	}
	return strings.HasPrefix(cleanTarget, cleanReserved+"/") || strings.HasPrefix(cleanReserved, cleanTarget+"/")
}

func shadowsContainerPath(target string, protected string) bool {
	cleanTarget := path.Clean(target)
	cleanProtected := path.Clean(protected)
	if cleanTarget == "/" || cleanTarget == cleanProtected {
		return true
	}
	return strings.HasPrefix(cleanProtected, cleanTarget+"/")
}

func configuredWorkspaceFolder(config map[string]any) string {
	value, _ := config["workspaceFolder"].(string)
	return value
}

func enforceProjectDevcontainerMode(file *manifest.File) error {
	if file == nil || len(file.RunModes) == 0 {
		return nil
	}
	if file.AllowsRunMode(manifest.RunModeProjectDevcontainer) {
		return nil
	}
	return fmt.Errorf("project manifest does not allow run mode %q", manifest.RunModeProjectDevcontainer)
}

func enforceProjectApprovals(file *manifest.File) error {
	if file == nil {
		return nil
	}
	if point, unsupported := file.UnsupportedApprovalPoint(); unsupported {
		return fmt.Errorf("project manifest declares approval point %q, but broker escalation approvals are not implemented; failing closed", point)
	}
	return nil
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
