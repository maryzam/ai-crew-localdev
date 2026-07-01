package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/maryzam/ai-crew-localdev/internal/config"
	"github.com/maryzam/ai-crew-localdev/internal/configstore"
	"github.com/maryzam/ai-crew-localdev/internal/identity"
	"github.com/maryzam/ai-crew-localdev/internal/policy"
	"github.com/maryzam/ai-crew-localdev/internal/securefile"
)

var policyInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Generate a default policy file from identities",
	Long: `Generate a default policy file from identities.

The generated file is a draft: it lists one agent per identity but leaves the
allowed resources empty, which is rejected by validation. Use --draft to write
the file anyway as a starting template, or run "ai-agent setup" for a fully
configured policy with discovered repositories.`,
	RunE: runPolicyInit,
}

var (
	initOutput     string
	initForce      bool
	initIdentities string
	initDraft      bool
)

func init() {
	policyInitCmd.Flags().StringVarP(&initOutput, "output", "o", "", "output path for policy file (default: ~/.config/ai-agent/policy.json)")
	policyInitCmd.Flags().BoolVar(&initForce, "force", false, "overwrite existing policy file")
	policyInitCmd.Flags().StringVar(&initIdentities, "identities", "", "path to identities file")
	policyInitCmd.Flags().BoolVar(&initDraft, "draft", false, "write the generated policy even if it does not pass validation")
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
	snapshot, err := configstore.Load(idPath, output)
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

	if !initForce {
		if _, err := os.Stat(output); err == nil {
			return fmt.Errorf("policy file already exists at %s (use --force to overwrite)", output)
		}
	}

	pf := policy.GenerateDefault(ids)
	result := policy.Validate(pf)
	if result.Errors.HasErrors() && !initDraft {
		writePolicyInitGuidance(cmd.ErrOrStderr(), output, result.Errors.Error())
		return fmt.Errorf("generated policy is incomplete; rerun with --draft to write it anyway, or run \"ai-agent setup\"")
	}

	if err := writePolicyFile(output, pf); err != nil {
		return err
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

func writePolicyFile(path string, pf *policy.PolicyFile) error {
	data, err := json.MarshalIndent(pf, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal policy: %w", err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}
	if err := securefile.WriteOwnerOnly(path, data); err != nil {
		return fmt.Errorf("write policy: %w", err)
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
