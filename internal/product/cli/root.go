package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/maryzam/ai-crew-localdev/internal/product/agents"
	"github.com/maryzam/ai-crew-localdev/internal/product/login"
	"github.com/spf13/cobra"
)

var Version = "dev"

type loginService interface {
	Login(context.Context, string) (login.Result, error)
}

func NewRoot(service loginService, out, errOut io.Writer) *cobra.Command {
	root := &cobra.Command{Use: "ai", Short: "Run native coding agents in a governed Podman environment", Version: Version, SilenceUsage: true, SilenceErrors: true}
	root.CompletionOptions.DisableDefaultCmd = true
	root.SetOut(out)
	root.SetErr(errOut)
	root.AddCommand(newLoginCommand(service, out))
	return root
}

func newLoginCommand(service loginService, out io.Writer) *cobra.Command {
	command := &cobra.Command{
		Use:       "login <claude|codex>",
		Short:     "Create a broker-only native subscription login",
		Args:      cobra.ExactArgs(1),
		ValidArgs: agentNames(),
		RunE: func(command *cobra.Command, args []string) error {
			result, err := service.Login(command.Context(), args[0])
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(out, "%s login stored for the broker at %s\n", result.Agent, result.Credential)
			if err == nil && result.RefreshBinary != "" {
				_, err = fmt.Fprintf(out, "Codex refresh binary pinned at %s with SHA-256 %s\n", result.RefreshBinary, result.BinarySHA256)
			}
			return err
		},
	}
	return command
}

func agentNames() []string {
	all := agents.All()
	names := make([]string, len(all))
	for index, agent := range all {
		names[index] = agent.Name
	}
	return names
}

func Execute() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	service := login.New(os.Stdin, os.Stdout, os.Stderr)
	root := NewRoot(service, os.Stdout, os.Stderr)
	root.SetContext(ctx)
	return root.Execute()
}
