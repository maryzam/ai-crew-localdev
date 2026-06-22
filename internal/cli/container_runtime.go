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
	args := append([]string{"devcontainer", "exec"}, devcontainerRuntimeArgs(runtime)...)
	args = append(args, "--workspace-folder", repoRoot, "bash")
	return shellCommand(args)
}

const fallbackShell = "if command -v bash >/dev/null 2>&1; then exec bash; else exec sh; fi"

func devcontainerExecShellCommand(workspace string, runtime containerRuntime, overlay []string) string {
	args := append([]string{"devcontainer", "exec"}, devcontainerRuntimeArgs(runtime)...)
	args = append(args, "--workspace-folder", workspace)
	args = append(args, overlay...)
	args = append(args, "sh", "-c", fallbackShell)
	return shellCommand(args)
}

func shellCommand(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = shellQuote(arg)
	}
	return strings.Join(quoted, " ")
}

func shellQuote(arg string) string {
	if isShellSafe(arg) {
		return arg
	}
	return "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
}

func isShellSafe(arg string) bool {
	if arg == "" {
		return false
	}
	for _, r := range arg {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case strings.ContainsRune("-_./:=@+,", r):
		default:
			return false
		}
	}
	return true
}
