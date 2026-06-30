package cli

import "github.com/maryzam/ai-crew-localdev/internal/devcontainer"

type containerRuntime devcontainer.Runtime

const (
	containerRuntimePodman containerRuntime = "podman"
	containerRuntimeDocker containerRuntime = "docker"
)

func parseContainerRuntime(value string) (containerRuntime, error) {
	runtime, err := devcontainer.ParseRuntime(value)
	return containerRuntime(runtime), err
}

func (r containerRuntime) binaryName() string {
	return devcontainer.Runtime(r).BinaryName()
}
