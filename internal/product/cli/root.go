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
	"golang.org/x/sys/unix"
)

var Version = "dev"

type loginService interface {
	Login(context.Context, string) (login.Credential, error)
}

func NewRoot(registry agents.Registry, service loginService, out, errOut io.Writer) *cobra.Command {
	root := &cobra.Command{Use: "ai", Short: "Run native coding agents in a governed Podman environment", Version: Version, SilenceUsage: true, SilenceErrors: true}
	root.CompletionOptions.DisableDefaultCmd = true
	root.SetOut(out)
	root.SetErr(errOut)
	root.AddCommand(newLoginCommand(registry, service, out))
	return root
}

func newLoginCommand(registry agents.Registry, service loginService, out io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:       "login <claude|codex>",
		Short:     "Create a broker-only native subscription login",
		Args:      cobra.ExactArgs(1),
		ValidArgs: registry.Names(),
		RunE: func(command *cobra.Command, args []string) error {
			credential, err := service.Login(command.Context(), args[0])
			if credential.Generation != "" {
				if _, printErr := fmt.Fprintf(out, "%s login published for the broker as generation %s\n", credential.Agent, credential.Generation); printErr != nil && err == nil {
					err = printErr
				}
			}
			if err == nil && credential.RefreshBinary != "" {
				_, err = fmt.Fprintf(out, "%s refresh binary pinned at %s with SHA-256 %s\n", credential.Agent, credential.RefreshBinary, credential.RefreshBinarySHA256)
			}
			return err
		},
	}
}

func Execute() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	registry := agents.Default()
	console := login.Console{In: os.Stdin, Out: os.Stdout, Err: os.Stderr, Terminal: isTerminal(os.Stdin) && isTerminal(os.Stdout)}
	root := NewRoot(registry, login.New(registry, console), os.Stdout, os.Stderr)
	root.SetContext(ctx)
	return root.Execute()
}

func isTerminal(file *os.File) bool {
	_, err := unix.IoctlGetTermios(int(file.Fd()), unix.TCGETS)
	return err == nil
}
