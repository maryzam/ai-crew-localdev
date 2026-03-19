package cli

import (
	"github.com/spf13/cobra"
)

// Version is set at build time via ldflags.
var Version = "dev"

var rootCmd = &cobra.Command{
	Use:     "ai-agent",
	Short:   "AI agent credential and policy management",
	Version: Version,
}

var policyCmd = &cobra.Command{
	Use:   "policy",
	Short: "Manage agent policy configuration",
}

func init() {
	rootCmd.AddCommand(policyCmd)
	policyCmd.AddCommand(policyInitCmd)
	policyCmd.AddCommand(policyValidateCmd)

	rootCmd.AddCommand(doctorCmd)
	rootCmd.AddCommand(runCmd)
	rootCmd.AddCommand(upCmd)

	rootCmd.AddCommand(sessionCmd)
	sessionCmd.AddCommand(sessionRevokeCmd)
	sessionCmd.AddCommand(sessionStatusCmd)
	sessionCmd.AddCommand(sessionListCmd)
}

// Execute runs the root command.
func Execute() error {
	return rootCmd.Execute()
}
