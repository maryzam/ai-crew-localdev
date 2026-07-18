//go:build integration

package e2e

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const readinessHomeDir = "/home/dev"
const readinessUsernsArg = "--userns=keep-id:uid=1000,gid=1000"

type readinessContainerRuntime interface {
	Name() string
	Binary() string
	BuildImage(t *testing.T, tag string)
	RemoveImage(t *testing.T, tag string)
	CreateVolume(t *testing.T, name string) string
	RemoveVolume(t *testing.T, name string)
	Run(t *testing.T, spec readinessRunSpec) ([]byte, error)
}

type readinessRunSpec struct {
	Workdir string
	Env     []string
	Mounts  []readinessMount
	Image   string
	Command []string
}

type readinessMount struct {
	Source   string
	Target   string
	ReadOnly bool
	Relabel  bool
}

type podmanReadinessRuntime struct {
	bin string
}

func newPodmanReadinessRuntime(t *testing.T) *podmanReadinessRuntime {
	t.Helper()

	podmanBin, err := lookPath("podman")
	if err != nil {
		t.Skipf("podman not available: %v", err)
	}
	return &podmanReadinessRuntime{bin: podmanBin}
}

func (r *podmanReadinessRuntime) Name() string {
	return "podman"
}

func (r *podmanReadinessRuntime) Binary() string {
	return r.bin
}

func (r *podmanReadinessRuntime) BuildImage(t *testing.T, tag string) {
	t.Helper()

	root := repoRoot(t)
	stageGenericBinary(t, root)
	t.Logf("building readiness image %s with %s", tag, r.Name())
	out, err := runCommandOutput(20*time.Minute, root, r.bin,
		"build", "--progress=plain", "-f", ".devcontainer/Dockerfile", "-t", tag, ".")
	if err != nil {
		t.Fatalf("%s build failed: %v\n%s", r.Name(), err, out)
	}
	t.Logf("built readiness image %s", tag)
}

func stageGenericBinary(t *testing.T, root string) {
	t.Helper()

	out, err := runCommandOutput(10*time.Minute, root, "make", "build")
	if err != nil {
		t.Fatalf("build ai-agent for the devcontainer build context: %v\n%s", err, out)
	}
}

func (r *podmanReadinessRuntime) RemoveImage(t *testing.T, tag string) {
	t.Helper()

	_, _ = runCommandOutput(2*time.Minute, repoRoot(t), r.bin, "rmi", "-f", tag)
}

func (r *podmanReadinessRuntime) CreateVolume(t *testing.T, name string) string {
	t.Helper()

	mustRunOutput(t, time.Minute, repoRoot(t), r.bin, "volume", "create", name)
	return name
}

func (r *podmanReadinessRuntime) RemoveVolume(t *testing.T, name string) {
	t.Helper()

	_, _ = runCommandOutput(time.Minute, repoRoot(t), r.bin, "volume", "rm", "-f", name)
}

func (r *podmanReadinessRuntime) Run(t *testing.T, spec readinessRunSpec) ([]byte, error) {
	t.Helper()

	args := []string{
		"run", "--rm",
		readinessUsernsArg,
		"--cap-drop=ALL",
		"--security-opt=no-new-privileges",
		"--read-only",
		"--tmpfs=/tmp:rw,noexec,nosuid,size=512m",
	}
	if spec.Workdir != "" {
		args = append(args, "-w", spec.Workdir)
	}
	for _, env := range spec.Env {
		args = append(args, "-e", env)
	}
	for _, mount := range spec.Mounts {
		args = append(args, "-v", mount.String())
	}
	args = append(args, spec.Image)
	args = append(args, spec.Command...)

	out, err := runCommandOutput(20*time.Minute, repoRoot(t), r.bin, args...)
	return []byte(out), err
}

func (m readinessMount) String() string {
	value := m.Source + ":" + m.Target
	var options []string
	if m.ReadOnly {
		options = append(options, "ro")
	}
	if m.Relabel {
		options = append(options, "Z")
	}
	if len(options) > 0 {
		value += ":" + strings.Join(options, ",")
	}
	return value
}

func mustRunOutput(t *testing.T, timeout time.Duration, dir string, name string, args ...string) string {
	t.Helper()

	out, err := runCommandOutput(timeout, dir, name, args...)
	if err != nil {
		t.Fatalf("%s %s failed: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return out
}

func runCommandOutput(timeout time.Duration, dir string, name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return out.String(), fmt.Errorf("%s timed out after %s: %w", name, timeout, ctx.Err())
	}
	return out.String(), err
}
