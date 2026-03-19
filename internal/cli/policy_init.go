package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/maryzam/ai-crew-localdev/internal/config"
	"github.com/maryzam/ai-crew-localdev/internal/identity"
	"github.com/maryzam/ai-crew-localdev/internal/policy"
)

var policyInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Generate a default policy file from identities",
	RunE:  runPolicyInit,
}

var (
	initOutput     string
	initForce      bool
	initIdentities string
)

func init() {
	policyInitCmd.Flags().StringVarP(&initOutput, "output", "o", "", "output path for policy file (default: ~/.config/ai-agent/policy.json)")
	policyInitCmd.Flags().BoolVar(&initForce, "force", false, "overwrite existing policy file")
	policyInitCmd.Flags().StringVar(&initIdentities, "identities", "", "path to identities file")
}

func runPolicyInit(cmd *cobra.Command, args []string) error {
	output := initOutput
	if output == "" {
		output = config.DefaultPolicyPath()
	}
	output = config.ExpandHome(output)

	idPath := initIdentities
	if idPath == "" {
		idPath = config.DefaultIdentitiesPath()
	}
	idPath = config.ExpandHome(idPath)

	// Load identities
	ids, err := identity.Load(idPath)
	if err != nil {
		return fmt.Errorf("failed to load identities from %s: %w", idPath, err)
	}

	// Validate identities
	if errs := identity.Validate(ids); errs.HasErrors() {
		return fmt.Errorf("identity validation failed: %w", errs)
	}

	// Check if output already exists
	if !initForce {
		if _, err := os.Stat(output); err == nil {
			return fmt.Errorf("policy file already exists at %s (use --force to overwrite)", output)
		}
	}

	// Generate default policy
	pf := policy.GenerateDefault(ids)

	// Marshal to JSON
	data, err := json.MarshalIndent(pf, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal policy: %w", err)
	}
	data = append(data, '\n')

	// Create parent directory if needed
	dir := filepath.Dir(output)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", dir, err)
	}

	// Write file
	if err := os.WriteFile(output, data, 0600); err != nil {
		return fmt.Errorf("failed to write policy file: %w", err)
	}

	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Policy file written to %s\n", output)
	_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Edit allowed_repos for each agent before use.\n")
	return nil
}
