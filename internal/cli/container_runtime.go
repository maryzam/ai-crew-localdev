package cli

import (
	"fmt"
	"strings"
)

type containerRuntime string

const (
	containerRuntimePodman containerRuntime = "podman"
	containerRuntimeDocker containerRuntime = "docker"
)

func parseContainerRuntime(value string) (containerRuntime, error) {
	switch containerRuntime(value) {
	case containerRuntimePodman, containerRuntimeDocker:
		return containerRuntime(value), nil
	default:
		return "", fmt.Errorf("invalid container runtime %q: expected podman or docker", value)
	}
}

func (r containerRuntime) binaryName() string {
	return string(r)
}

func (r containerRuntime) alternate() containerRuntime {
	switch r {
	case containerRuntimePodman:
		return containerRuntimeDocker
	case containerRuntimeDocker:
		return containerRuntimePodman
	default:
		return ""
	}
}

func devcontainerRuntimeArgs(runtime containerRuntime) []string {
	return []string{"--docker-path", runtime.binaryName()}
}

func devcontainerExecCommand(repoRoot string, runtime containerRuntime) string {
	return fmt.Sprintf("devcontainer exec --docker-path %s --workspace-folder %s bash", runtime.binaryName(), repoRoot)
}

// fallbackShell opens bash when the image provides it and POSIX sh otherwise,
// so an arbitrary project base (e.g. Alpine) still gets an interactive shell.
const fallbackShell = "if command -v bash >/dev/null 2>&1; then exec bash; else exec sh; fi"

// devcontainerExecShellCommand is the project-mode re-enter hint. It uses the
// bash/sh fallback because the project's own image may not ship bash.
func devcontainerExecShellCommand(workspace string, runtime containerRuntime, overlay []string) string {
	args := append([]string{"devcontainer", "exec"}, devcontainerRuntimeArgs(runtime)...)
	args = append(args, "--workspace-folder", workspace)
	args = append(args, overlay...)
	return fmt.Sprintf("%s sh -c %q", strings.Join(args, " "), fallbackShell)
}
