package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/maryzam/ai-crew-localdev/internal/config"
	"github.com/maryzam/ai-crew-localdev/internal/policy"
)

var policyValidateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Validate a policy file",
	RunE:  runPolicyValidate,
}

var validatePolicyPath string

func init() {
	policyValidateCmd.Flags().StringVar(&validatePolicyPath, "policy", "", "path to policy file (default: ~/.config/ai-agent/policy.json)")
}

func runPolicyValidate(cmd *cobra.Command, args []string) error {
	policyPath := validatePolicyPath
	if policyPath == "" {
		policyPath = config.DefaultPolicyPath()
	}
	policyPath = config.ExpandHome(policyPath)

	data, err := os.ReadFile(policyPath)
	if err != nil {
		return fmt.Errorf("failed to read policy file %s: %w", policyPath, err)
	}

	pf, err := policy.ParsePolicy(data)
	if err != nil {
		return fmt.Errorf("failed to parse policy file: %w", err)
	}

	result := policy.Validate(pf)

	for _, w := range result.Warnings {
		fmt.Fprintf(cmd.OutOrStderr(), "WARNING: %s: %s\n", w.Field, w.Message)
	}

	if result.Errors.HasErrors() {
		fmt.Fprintf(cmd.OutOrStderr(), "Validation failed with %d error(s):\n", len(result.Errors))
		for _, e := range result.Errors {
			fmt.Fprintf(cmd.OutOrStderr(), "  - %s: %s\n", e.Field, e.Message)
		}
		return fmt.Errorf("policy validation failed")
	}

	fmt.Fprintf(cmd.OutOrStdout(), "OK: policy file %s is valid\n", policyPath)
	return nil
}
