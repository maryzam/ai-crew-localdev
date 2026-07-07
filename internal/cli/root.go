package cli

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
)

var Version = "dev"

func NewRoot(services ProviderServices) (*cobra.Command, error) {
	if err := services.Validate(); err != nil {
		return nil, err
	}
	root := &cobra.Command{Use: "ai-agent", Short: "AI agent credential and policy management", Version: Version}
	policyCommand := &cobra.Command{Use: "policy", Short: "Manage agent policy configuration"}
	policyCommand.AddCommand(policyInitCmd, newPolicyValidateCommand(services.ValidatePolicy))
	root.AddCommand(policyCommand)
	root.AddCommand(newDoctorCommand(newReadinessService(services.ValidatePolicy)))
	root.AddCommand(newAuthCommand())
	root.AddCommand(bootstrapCmd)
	root.AddCommand(checkCmd)
	root.AddCommand(installCmd)
	root.AddCommand(runCmd)
	root.AddCommand(newSetupCommand(services))
	root.AddCommand(newUpCommand(services))
	root.AddCommand(runsCmd)
	root.AddCommand(sessionCmd)
	sessionCmd.AddCommand(sessionRevokeCmd)
	sessionCmd.AddCommand(sessionStatusCmd)
	sessionCmd.AddCommand(sessionListCmd)
	return root, nil
}

func Execute(services ProviderServices) error {
	root, err := NewRoot(services)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	root.SetContext(ctx)
	return root.Execute()
}
