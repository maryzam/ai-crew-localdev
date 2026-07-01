package cli

import "github.com/maryzam/ai-crew-localdev/internal/runtime/devcontainer"

type containerRuntime = devcontainer.Runtime

const (
	containerRuntimePodman = devcontainer.Podman
	containerRuntimeDocker = devcontainer.Docker
)

func parseContainerRuntime(value string) (containerRuntime, error) {
	return devcontainer.ParseRuntime(value)
}
