package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/maryzam/ai-crew-localdev/internal/brokerd"
	"github.com/maryzam/ai-crew-localdev/internal/cli"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/identity"
	"github.com/maryzam/ai-crew-localdev/internal/governance/policycheck"
	"github.com/maryzam/ai-crew-localdev/internal/platform/paths"
	githubprovider "github.com/maryzam/ai-crew-localdev/internal/providers/github"
	"github.com/maryzam/ai-crew-localdev/internal/shim/credentialhelper"
	"github.com/maryzam/ai-crew-localdev/internal/shim/ghwrapper"
)

func main() {
	switch filepath.Base(os.Args[0]) {
	case "ai-agent-broker":
		if err := brokerd.Run(); err != nil {
			log.Fatalf("ai-agent-broker: %v", err)
		}
	case "gh", "ai-agent-gh":
		if err := ghwrapper.Run(os.Args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "ai-agent-gh: %v\n", err)
			os.Exit(1)
		}
	case "ai-agent-credential-helper":
		if err := credentialhelper.Run(os.Args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "ai-agent-credential-helper: %v\n", err)
			os.Exit(1)
		}
	default:
		runCLI()
	}
}

func runCLI() {
	githubClient := githubprovider.NewGitHubClient(os.Getenv(paths.EnvGitHubBaseURL))
	services := cli.ProviderServices{
		GitHubClient: githubClient,
		NewSigner: func(identities *identity.IdentitiesFile) (cli.JWTSigner, error) {
			return githubprovider.NewSigner(identities)
		},
		ValidatePolicy: policycheck.Validate,
	}
	if err := cli.Execute(services); err != nil {
		os.Exit(1)
	}
}
