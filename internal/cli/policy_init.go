package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/maryzam/ai-crew-localdev/internal/configmodel/governance"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/identity"
	"github.com/maryzam/ai-crew-localdev/internal/configmodel/policy"
)

type policyInitOptions struct {
	output     string
	force      bool
	identities string
	draft      bool
}

func newPolicyInitCommand() *cobra.Command {
	options := policyInitOptions{}
	command := &cobra.Command{
		Use:   "init",
		Short: "Generate a default policy file from identities",
		Long: `Generate a default policy file from identities.

The generated file is a draft: it lists one agent per identity but leaves the
allowed resources empty, which is rejected by validation. Use --draft to write
the file anyway as a starting template, or run "ai-agent setup" for a fully
configured policy with discovered repositories.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runPolicyInit(cmd, options)
		},
	}
	command.Flags().StringVarP(&options.output, "output", "o", options.output, "output path for policy file (default: governance policy path)")
	command.Flags().BoolVar(&options.force, "force", options.force, "overwrite existing policy file")
	command.Flags().StringVar(&options.identities, "identities", options.identities, "path to identities file")
	command.Flags().BoolVar(&options.draft, "draft", options.draft, "write the generated policy even if it does not pass validation")
	return command
}

func runPolicyInit(cmd *cobra.Command, options policyInitOptions) error {
	governancePaths := governancePathsFromOverrides(options.identities, options.output)
	output := governancePaths.Policy
	idPath := governancePaths.Identities
	governanceStore := governance.FileStore{}
	snapshot, err := governanceStore.Load(governancePaths)
	if err != nil {
		return fmt.Errorf("load identities from %s: %w", idPath, err)
	}
	if snapshot.IdentitiesError != nil {
		return fmt.Errorf("load identities from %s: %w", idPath, snapshot.IdentitiesError)
	}
	ids := snapshot.Identities
	if errs := identity.Validate(ids); errs.HasErrors() {
		return fmt.Errorf("identity validation failed: %w", errs)
	}

	if !options.force {
		if _, err := os.Stat(output); err == nil {
			return fmt.Errorf("policy file already exists at %s (use --force to overwrite)", output)
		}
	}

	pf := policy.GenerateDefault(ids)
	result := policy.Validate(pf)
	if result.Errors.HasErrors() && !options.draft {
		writePolicyInitGuidance(cmd.ErrOrStderr(), output, result.Errors.Error())
		return fmt.Errorf("generated policy is incomplete; rerun with --draft to write it anyway, or run \"ai-agent setup\"")
	}

	if err := governanceStore.PublishPolicy(governancePaths, pf); err != nil {
		return fmt.Errorf("publish policy: %w", err)
	}

	out := cmd.OutOrStdout()
	_, _ = fmt.Fprintf(out, "Policy file written to %s\n", output)
	if result.Errors.HasErrors() {
		_, _ = fmt.Fprintln(out, "")
		_, _ = fmt.Fprintln(out, "This file is a draft and will be rejected by the broker until you fix:")
		_, _ = fmt.Fprintln(out, result.Errors.Error())
		_, _ = fmt.Fprintln(out, "")
		_, _ = fmt.Fprintln(out, "Tip: \"ai-agent setup\" discovers repositories via GitHub and writes a ready-to-use policy.")
	}
	return nil
}

func writePolicyInitGuidance(w io.Writer, output, errors string) {
	_, _ = fmt.Fprintln(w, "Refusing to write a policy that would be rejected by the broker.")
	_, _ = fmt.Fprintln(w, "")
	_, _ = fmt.Fprintln(w, "Missing/invalid:")
	_, _ = fmt.Fprintln(w, errors)
	_, _ = fmt.Fprintln(w, "")
	_, _ = fmt.Fprintln(w, "Options:")
	_, _ = fmt.Fprintln(w, "  - Run \"ai-agent setup\" to discover repositories and write a complete policy.")
	_, _ = fmt.Fprintf(w, "  - Rerun \"ai-agent policy init --draft\" to write the incomplete file to %s and edit it by hand.\n", output)
}
