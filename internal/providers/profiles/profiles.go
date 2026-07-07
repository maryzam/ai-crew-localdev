package profiles

import (
	"github.com/maryzam/ai-crew-localdev/internal/interception"
	githubcontract "github.com/maryzam/ai-crew-localdev/internal/providers/github/contract"
	langfusecontract "github.com/maryzam/ai-crew-localdev/internal/providers/langfuse/contract"
)

func All() []interception.Profile {
	return []interception.Profile{
		githubcontract.InterceptionProfile(),
		langfusecontract.InterceptionProfile(),
	}
}

func Commands() []string {
	var commands []string
	for _, profile := range All() {
		commands = append(commands, profile.Commands...)
	}
	return commands
}
