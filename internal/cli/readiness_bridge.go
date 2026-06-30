package cli

import (
	"os"

	"github.com/maryzam/ai-crew-localdev/internal/identity"
	"github.com/maryzam/ai-crew-localdev/internal/policy"
	"github.com/maryzam/ai-crew-localdev/internal/readiness"
)

type doctorMode = readiness.Mode
type doctorCheck = readiness.Check
type doctorReport = readiness.Report

const (
	doctorModeUp     = readiness.ModeUp
	doctorStatusFail = readiness.StatusFail
)

func buildDoctorReport(service readiness.Service, mode doctorMode, socketPath, repoPath string, runtime containerRuntime) doctorReport {
	return service.Run(readinessInput(mode, socketPath, repoPath, runtime))
}

func checkRuntimeDir(service readiness.Service, path string) doctorCheck {
	source := "fallback"
	if os.Getenv("XDG_RUNTIME_DIR") != "" {
		source = "XDG_RUNTIME_DIR"
	}
	return service.RuntimeDir(path, source)
}

func checkBinaryReadinessForUp(service readiness.Service) []doctorCheck {
	return service.Binaries(true)
}

func checkContainerWorkspace(service readiness.Service) doctorCheck {
	return service.Workspace(os.Getenv("AI_AGENT_WORKSPACE"))
}

func checkContainerRuntime(service readiness.Service, runtime containerRuntime) doctorCheck {
	return service.ContainerRuntime(runtime.binaryName())
}

func hasBlockingFailure(checks []doctorCheck) bool {
	return readiness.HasBlockingFailure(checks)
}

func loadedIdentitiesCheck(path string, identities *identity.IdentitiesFile, err error) (*identity.IdentitiesFile, doctorCheck) {
	return readiness.Identities(path, identities, err)
}

func loadedPolicyCheck(path string, policyFile *policy.PolicyFile, err error) (*policy.PolicyFile, doctorCheck) {
	return readiness.Policy(path, policyFile, err)
}

func checkIdentityKeys(service readiness.Service, identities identity.IdentitiesFile) []doctorCheck {
	return service.IdentityKeys(identities)
}

func checkPolicyProviderConfig(service readiness.Service, identities *identity.IdentitiesFile, policyFile *policy.PolicyFile, path string) doctorCheck {
	return service.PolicyProviders(identities, policyFile, path)
}

func checkInstallationIDs(identities identity.IdentitiesFile, policyFile policy.PolicyFile, path string) doctorCheck {
	return readiness.InstallationIDs(identities, policyFile, path)
}
