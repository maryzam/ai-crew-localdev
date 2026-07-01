package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/maryzam/ai-crew-localdev/internal/configmodel/identity"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/policy"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/store"
	"github.com/maryzam/ai-crew-localdev/internal/platform/paths"
)

type policyValidateOptions struct {
	policyPath     string
	identitiesPath string
}

func newPolicyValidateCommand(validator func(*policy.PolicyFile, *identity.IdentitiesFile) error) *cobra.Command {
	options := policyValidateOptions{}
	command := &cobra.Command{Use: "validate", Short: "Validate a policy file (schema + provider config)"}
	command.Flags().StringVar(&options.policyPath, "policy", "", "path to policy file (default: ~/.config/ai-agent/policy.json)")
	command.Flags().StringVar(&options.identitiesPath, "identities", "", "path to identities file (default: ~/.config/ai-agent/identities.json)")
	command.RunE = func(command *cobra.Command, args []string) error {
		return runPolicyValidate(command, options, validator)
	}
	return command
}

func runPolicyValidate(cmd *cobra.Command, options policyValidateOptions, validator func(*policy.PolicyFile, *identity.IdentitiesFile) error) error {
	policyPath := resolvedPath(options.policyPath, paths.DefaultPolicyPath())
	identitiesPath := resolvedPath(options.identitiesPath, paths.DefaultIdentitiesPath())
	snapshot, err := store.Load(identitiesPath, policyPath)
	if err != nil {
		return fmt.Errorf("inspect governance configuration: %w", err)
	}
	if snapshot.PolicyError != nil {
		return fmt.Errorf("failed to load policy file %s: %w", policyPath, snapshot.PolicyError)
	}
	pf := snapshot.Policy
	if result := policy.Validate(pf); result.Errors.HasErrors() {
		writePolicyValidationErrors(cmd, result.Errors)
		return fmt.Errorf("policy schema validation failed")
	} else {
		writePolicyValidationWarnings(cmd, result.Warnings)
	}

	if snapshot.IdentitiesError != nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Identities validation failed:\n  %s\n", snapshot.IdentitiesError)
		return fmt.Errorf("identity validation failed")
	}
	idents := snapshot.Identities
	if errs := identity.Validate(idents); errs.HasErrors() {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Identities validation failed:\n  identities file %s is invalid: %s\n", identitiesPath, errs.Error())
		return fmt.Errorf("identity validation failed")
	}
	if err := validator(pf, idents); err != nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Provider validation failed:\n  %s\n", err)
		return fmt.Errorf("policy provider validation failed")
	}

	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "OK: policy file %s is valid (schema + provider config)\n", policyPath)
	return nil
}

func resolvedPath(override, fallback string) string {
	if override != "" {
		return paths.ExpandHome(override)
	}
	return paths.ExpandHome(fallback)
}

func writePolicyValidationErrors(cmd *cobra.Command, errs interface{ Error() string }) {
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Validation failed:\n  %s\n", errs.Error())
}

func writePolicyValidationWarnings(cmd *cobra.Command, warnings []policy.Warning) {
	for _, w := range warnings {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "WARNING: %s: %s\n", w.Field, w.Message)
	}
}
