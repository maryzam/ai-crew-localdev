package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/maryzam/ai-crew-localdev/internal/broker"
	"github.com/maryzam/ai-crew-localdev/internal/config"
	"github.com/maryzam/ai-crew-localdev/internal/identity"
	"github.com/maryzam/ai-crew-localdev/internal/policy"
)

var policyValidateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Validate a policy file (schema + provider config)",
	RunE:  runPolicyValidate,
}

var (
	validatePolicyPath     string
	validateIdentitiesPath string
)

func init() {
	policyValidateCmd.Flags().StringVar(&validatePolicyPath, "policy", "", "path to policy file (default: ~/.config/ai-agent/policy.json)")
	policyValidateCmd.Flags().StringVar(&validateIdentitiesPath, "identities", "", "path to identities file (default: ~/.config/ai-agent/identities.json)")
}

func runPolicyValidate(cmd *cobra.Command, args []string) error {
	policyPath := resolvedPath(validatePolicyPath, config.DefaultPolicyPath())
	identitiesPath := resolvedPath(validateIdentitiesPath, config.DefaultIdentitiesPath())

	pf, err := readPolicyFile(policyPath)
	if err != nil {
		return err
	}
	if result := policy.Validate(pf); result.Errors.HasErrors() {
		writePolicyValidationErrors(cmd, result.Errors)
		return fmt.Errorf("policy schema validation failed")
	} else {
		writePolicyValidationWarnings(cmd, result.Warnings)
	}

	idents, err := loadIdentitiesForValidation(identitiesPath)
	if err != nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Identities validation failed:\n  %s\n", err)
		return fmt.Errorf("identity validation failed")
	}
	if err := broker.ValidatePolicy(pf, validatorProviders(idents)); err != nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Provider validation failed:\n  %s\n", err)
		return fmt.Errorf("policy provider validation failed")
	}

	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "OK: policy file %s is valid (schema + provider config)\n", policyPath)
	return nil
}

func resolvedPath(override, fallback string) string {
	if override != "" {
		return config.ExpandHome(override)
	}
	return config.ExpandHome(fallback)
}

func readPolicyFile(path string) (*policy.PolicyFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read policy file %s: %w", path, err)
	}
	pf, err := policy.ParsePolicy(data)
	if err != nil {
		return nil, fmt.Errorf("failed to parse policy file: %w", err)
	}
	return pf, nil
}

func loadIdentitiesForValidation(path string) (*identity.IdentitiesFile, error) {
	idents, err := identity.Load(path)
	if err != nil {
		return nil, fmt.Errorf("failed to load identities file %s: %w", path, err)
	}
	if errs := identity.Validate(idents); errs.HasErrors() {
		return nil, fmt.Errorf("identities file %s is invalid: %s", path, errs.Error())
	}
	return idents, nil
}

func writePolicyValidationErrors(cmd *cobra.Command, errs interface{ Error() string }) {
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "Validation failed:\n  %s\n", errs.Error())
}

func writePolicyValidationWarnings(cmd *cobra.Command, warnings []policy.Warning) {
	for _, w := range warnings {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "WARNING: %s: %s\n", w.Field, w.Message)
	}
}
