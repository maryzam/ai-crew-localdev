package doctor

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/maryzam/ai-crew-localdev/internal/config"
	"github.com/maryzam/ai-crew-localdev/internal/identity"
	"github.com/maryzam/ai-crew-localdev/internal/policy"
)

// CheckStatus represents the outcome of a diagnostic check.
type CheckStatus int

const (
	StatusPass CheckStatus = iota
	StatusWarn
	StatusFail
)

// CheckResult holds the outcome of a single diagnostic check.
type CheckResult struct {
	Name    string
	Status  CheckStatus
	Message string
	Detail  string
}

// CheckIdentitiesFile verifies that identities.json exists, is readable,
// parses correctly, and has a valid schema version.
func CheckIdentitiesFile(configDir string) CheckResult {
	path := filepath.Join(configDir, "identities.json")

	ids, err := identity.Load(path)
	if err != nil {
		return CheckResult{
			Name:    "identities file",
			Status:  StatusFail,
			Message: "cannot load identities file",
			Detail:  err.Error(),
		}
	}

	errs := identity.Validate(ids)
	if errs.HasErrors() {
		return CheckResult{
			Name:    "identities file",
			Status:  StatusFail,
			Message: "identities file has validation errors",
			Detail:  errs.Error(),
		}
	}

	return CheckResult{
		Name:    "identities file",
		Status:  StatusPass,
		Message: fmt.Sprintf("identities file loaded (%d agents)", len(ids.Agents)),
	}
}

// CheckPolicyFile verifies that policy.json exists, is readable, and validates.
// A missing policy file produces a warning since it is optional until the broker runs.
func CheckPolicyFile(configDir string) CheckResult {
	path := filepath.Join(configDir, "policy.json")

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return CheckResult{
				Name:    "policy file",
				Status:  StatusWarn,
				Message: "policy file not found (run 'ai-agent policy init')",
			}
		}
		return CheckResult{
			Name:    "policy file",
			Status:  StatusFail,
			Message: "cannot read policy file",
			Detail:  err.Error(),
		}
	}

	pf, err := policy.ParsePolicy(data)
	if err != nil {
		return CheckResult{
			Name:    "policy file",
			Status:  StatusFail,
			Message: "cannot parse policy file",
			Detail:  err.Error(),
		}
	}

	result := policy.Validate(pf)
	if result.Errors.HasErrors() {
		return CheckResult{
			Name:    "policy file",
			Status:  StatusFail,
			Message: "policy file has validation errors",
			Detail:  result.Errors.Error(),
		}
	}

	return CheckResult{
		Name:    "policy file",
		Status:  StatusPass,
		Message: fmt.Sprintf("policy file valid (%d agents)", len(pf.Agents)),
	}
}

// CheckPEMFiles verifies that each agent's PEM key file exists, is readable,
// and has appropriately restrictive file permissions (0600 or 0400).
func CheckPEMFiles(configDir string) []CheckResult {
	path := filepath.Join(configDir, "identities.json")
	ids, err := identity.Load(path)
	if err != nil {
		return []CheckResult{{
			Name:    "PEM files",
			Status:  StatusFail,
			Message: "cannot check PEM files (identities file not loadable)",
			Detail:  err.Error(),
		}}
	}

	var results []CheckResult
	for name, agent := range ids.Agents {
		pemPath := config.ExpandHome(agent.AppKey)
		if !filepath.IsAbs(pemPath) {
			pemPath = filepath.Join(configDir, pemPath)
		}

		info, err := os.Stat(pemPath)
		if err != nil {
			if os.IsNotExist(err) {
				results = append(results, CheckResult{
					Name:    fmt.Sprintf("PEM file for %s", name),
					Status:  StatusFail,
					Message: fmt.Sprintf("PEM file not found: %s", pemPath),
				})
			} else {
				results = append(results, CheckResult{
					Name:    fmt.Sprintf("PEM file for %s", name),
					Status:  StatusFail,
					Message: fmt.Sprintf("cannot stat PEM file: %s", pemPath),
					Detail:  err.Error(),
				})
			}
			continue
		}

		mode := info.Mode().Perm()
		if mode == 0o600 || mode == 0o400 {
			results = append(results, CheckResult{
				Name:    fmt.Sprintf("PEM file for %s", name),
				Status:  StatusPass,
				Message: fmt.Sprintf("PEM file for %s (mode %04o)", name, mode),
			})
		} else {
			results = append(results, CheckResult{
				Name:    fmt.Sprintf("PEM file for %s", name),
				Status:  StatusWarn,
				Message: fmt.Sprintf("PEM file for %s has mode %04o, expected 0600 or 0400", name, mode),
			})
		}
	}

	return results
}

// CheckAppIDs verifies that every agent in identities.json has a non-empty app_id.
func CheckAppIDs(configDir string) CheckResult {
	path := filepath.Join(configDir, "identities.json")
	ids, err := identity.Load(path)
	if err != nil {
		return CheckResult{
			Name:    "app_id configuration",
			Status:  StatusFail,
			Message: "cannot check app_id (identities file not loadable)",
			Detail:  err.Error(),
		}
	}

	var missing []string
	for name, agent := range ids.Agents {
		if agent.AppID == "" {
			missing = append(missing, name)
		}
	}

	if len(missing) > 0 {
		return CheckResult{
			Name:    "app_id configuration",
			Status:  StatusFail,
			Message: fmt.Sprintf("app_id missing for agents: %v", missing),
		}
	}

	return CheckResult{
		Name:    "app_id configuration",
		Status:  StatusPass,
		Message: "app_id configured for all agents",
	}
}

// CheckBrokerSocketDir verifies that the RuntimeDir parent exists and is writable,
// and that the ai-agent subdirectory can be created with mode 0700.
func CheckBrokerSocketDir(runtimeDir string) CheckResult {
	parentDir := filepath.Dir(runtimeDir)

	info, err := os.Stat(parentDir)
	if err != nil {
		return CheckResult{
			Name:    "broker socket directory",
			Status:  StatusFail,
			Message: fmt.Sprintf("runtime parent directory does not exist: %s", parentDir),
			Detail:  err.Error(),
		}
	}

	if !info.IsDir() {
		return CheckResult{
			Name:    "broker socket directory",
			Status:  StatusFail,
			Message: fmt.Sprintf("runtime parent path is not a directory: %s", parentDir),
		}
	}

	// Check if we can create or access the ai-agent subdirectory.
	err = os.MkdirAll(runtimeDir, 0o700)
	if err != nil {
		return CheckResult{
			Name:    "broker socket directory",
			Status:  StatusFail,
			Message: fmt.Sprintf("cannot create runtime directory: %s", runtimeDir),
			Detail:  err.Error(),
		}
	}

	// Verify the directory is writable by creating and removing a temp file.
	testFile := filepath.Join(runtimeDir, ".doctor-probe")
	f, err := os.Create(testFile)
	if err != nil {
		return CheckResult{
			Name:    "broker socket directory",
			Status:  StatusFail,
			Message: fmt.Sprintf("runtime directory not writable: %s", runtimeDir),
			Detail:  err.Error(),
		}
	}
	f.Close()
	os.Remove(testFile)

	return CheckResult{
		Name:    "broker socket directory",
		Status:  StatusPass,
		Message: "broker socket directory writable",
	}
}

// CheckSystemdUser verifies that systemd --user is available.
// A missing systemctl binary is a failure. A non-zero exit code from
// "systemctl --user status" is still a pass (non-zero just means no active units).
func CheckSystemdUser() CheckResult {
	_, err := exec.LookPath("systemctl")
	if err != nil {
		return CheckResult{
			Name:    "systemd --user",
			Status:  StatusWarn,
			Message: "systemctl not found in PATH",
			Detail:  "socket activation requires systemd --user",
		}
	}

	cmd := exec.Command("systemctl", "--user", "status")
	err = cmd.Run()
	if err != nil {
		// Check if this is an ExitError (command ran but returned non-zero) vs a real error.
		if _, ok := err.(*exec.ExitError); ok {
			// Non-zero exit is fine; systemctl --user status returns non-zero
			// when no units are active.
			return CheckResult{
				Name:    "systemd --user",
				Status:  StatusPass,
				Message: "systemd --user available",
			}
		}
		return CheckResult{
			Name:    "systemd --user",
			Status:  StatusWarn,
			Message: "systemd --user may not be available",
			Detail:  err.Error(),
		}
	}

	return CheckResult{
		Name:    "systemd --user",
		Status:  StatusPass,
		Message: "systemd --user available",
	}
}

// CheckAllowedRepos warns if any agent in the policy file has empty allowed_repos.
func CheckAllowedRepos(configDir string) CheckResult {
	path := filepath.Join(configDir, "policy.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			// No policy file; skip this check since it's optional.
			return CheckResult{
				Name:    "allowed repos",
				Status:  StatusWarn,
				Message: "cannot check allowed_repos (no policy file)",
			}
		}
		return CheckResult{
			Name:    "allowed repos",
			Status:  StatusFail,
			Message: "cannot read policy file",
			Detail:  err.Error(),
		}
	}

	pf, err := policy.ParsePolicy(data)
	if err != nil {
		return CheckResult{
			Name:    "allowed repos",
			Status:  StatusFail,
			Message: "cannot parse policy file",
			Detail:  err.Error(),
		}
	}

	var empty []string
	for name, agent := range pf.Agents {
		if len(agent.AllowedRepos) == 0 {
			empty = append(empty, name)
		}
	}

	if len(empty) > 0 {
		return CheckResult{
			Name:    "allowed repos",
			Status:  StatusWarn,
			Message: fmt.Sprintf("agents with empty allowed_repos: %v", empty),
			Detail:  "configure allowed_repos in policy.json before running the broker",
		}
	}

	return CheckResult{
		Name:    "allowed repos",
		Status:  StatusPass,
		Message: "all agents have allowed_repos configured",
	}
}
