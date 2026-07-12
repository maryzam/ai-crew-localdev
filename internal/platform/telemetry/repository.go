package telemetry

import (
	"os/exec"
	"strings"
)

func inspectRepository(rootPath, slug string) RepositoryMetadata {
	metadata := RepositoryMetadata{
		Slug:       boundedField("ai_agent.repository.slug", slug),
		RemoteHost: "github.com",
		RootPath:   boundedField("ai_agent.repository.root_path", rootPath),
	}
	metadata.CommitSHA = boundedField("ai_agent.repository.commit", gitOutput(rootPath, "rev-parse", "HEAD"))
	metadata.Branch = boundedField("ai_agent.repository.branch", gitOutput(rootPath, "branch", "--show-current"))
	metadata.Dirty = gitOutput(rootPath, "status", "--porcelain") != ""
	return metadata
}

func gitOutput(rootPath string, args ...string) string {
	commandArgs := append([]string{"-C", rootPath}, args...)
	data, err := exec.Command("git", commandArgs...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
