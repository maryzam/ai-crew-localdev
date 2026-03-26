package cli

import "fmt"

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
