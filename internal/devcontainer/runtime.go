package devcontainer

import (
	"fmt"
	"strings"
)

type Runtime string

const (
	Podman Runtime = "podman"
	Docker Runtime = "docker"
)

func ParseRuntime(value string) (Runtime, error) {
	runtime := Runtime(value)
	if runtime != Podman && runtime != Docker {
		return "", fmt.Errorf("invalid container runtime %q: expected podman or docker", value)
	}
	return runtime, nil
}

func (r Runtime) BinaryName() string {
	return string(r)
}

func RuntimeArgs(runtime Runtime) []string {
	return []string{"--docker-path", runtime.BinaryName()}
}

func ExecCommand(workspace string, runtime Runtime) string {
	args := append([]string{"devcontainer", "exec"}, RuntimeArgs(runtime)...)
	args = append(args, "--workspace-folder", workspace, "bash")
	return ShellCommand(args)
}

const FallbackShell = "if command -v bash >/dev/null 2>&1; then exec bash; else exec sh; fi"

func ExecShellCommand(workspace string, runtime Runtime, overlay []string) string {
	args := append([]string{"devcontainer", "exec"}, RuntimeArgs(runtime)...)
	args = append(args, "--workspace-folder", workspace)
	args = append(args, overlay...)
	args = append(args, "sh", "-c", FallbackShell)
	return ShellCommand(args)
}

func UpArgs(runtime Runtime, workspace string, overlay []string, build bool) []string {
	args := append([]string{"up"}, RuntimeArgs(runtime)...)
	args = append(args, "--workspace-folder", workspace)
	args = append(args, overlay...)
	if build {
		args = append(args, "--build-no-cache")
	}
	return args
}

func ProjectExecArgs(runtime Runtime, workspace string, overlay []string, command ...string) []string {
	args := append([]string{"exec"}, RuntimeArgs(runtime)...)
	args = append(args, "--workspace-folder", workspace)
	args = append(args, overlay...)
	return append(args, command...)
}

func ShellCommand(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = shellQuote(arg)
	}
	return strings.Join(quoted, " ")
}

func shellQuote(arg string) string {
	if isShellSafe(arg) {
		return arg
	}
	return "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
}

func isShellSafe(arg string) bool {
	if arg == "" {
		return false
	}
	for _, r := range arg {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case strings.ContainsRune("-_./:=@+,", r):
		default:
			return false
		}
	}
	return true
}
