package cli

import (
	"bytes"
	"github.com/maryzam/ai-crew-localdev/internal/platform/paths"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func withInstallSeams(t *testing.T, dir string) *[][]string {
	t.Helper()
	origDir := installUnitDir
	origBrokerPath := installBrokerPath
	origRun := installRunCmd
	t.Cleanup(func() {
		installUnitDir = origDir
		installBrokerPath = origBrokerPath
		installRunCmd = origRun
	})

	var calls [][]string
	installUnitDir = func() (string, error) { return dir, nil }
	installBrokerPath = func() (string, error) { return "/opt/ai-agent/bin/ai-agent-broker", nil }
	installRunCmd = func(c *exec.Cmd) error {
		calls = append(calls, c.Args)
		return nil
	}
	return &calls
}

func newInstallCmd() (*cobra.Command, *bytes.Buffer) {
	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	return cmd, &buf
}

func TestInstallWritesUnitsAndEnablesSocket(t *testing.T) {
	dir := t.TempDir()
	calls := withInstallSeams(t, dir)

	cmd, _ := newInstallCmd()
	if err := runInstall(cmd, installOptions{}); err != nil {
		t.Fatalf("runInstall: %v", err)
	}

	wantUnits, err := brokerUnitsWithService(brokerServiceUnitPrefix + "/opt/ai-agent/bin/ai-agent-broker" + brokerServiceUnitSuffix)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range wantUnits {
		got, err := os.ReadFile(filepath.Join(dir, u.name))
		if err != nil {
			t.Fatalf("read %s: %v", u.name, err)
		}
		if string(got) != u.contents {
			t.Errorf("%s contents mismatch", u.name)
		}
	}

	want := [][]string{
		{"systemctl", "--user", "daemon-reload"},
		{"systemctl", "--user", "enable", brokerSocketUnitName},
	}
	if len(*calls) != len(want) {
		t.Fatalf("expected %d systemctl calls, got %d: %v", len(want), len(*calls), *calls)
	}
	for i, w := range want {
		if strings.Join((*calls)[i], " ") != strings.Join(w, " ") {
			t.Errorf("call %d = %v, want %v", i, (*calls)[i], w)
		}
	}
}

func TestInstallCreatesMissingUnitDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "user")
	withInstallSeams(t, dir)

	cmd, _ := newInstallCmd()
	if err := runInstall(cmd, installOptions{}); err != nil {
		t.Fatalf("runInstall: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, brokerSocketUnitName)); err != nil {
		t.Errorf("socket unit not written: %v", err)
	}
}

func TestUninstallRemovesUnitsAndDisablesSocket(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(paths.EnvBrokerSocket, "")
	units, err := brokerUnitsWithService(brokerServiceUnit)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range units {
		if err := os.WriteFile(filepath.Join(dir, u.name), []byte(u.contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	calls := withInstallSeams(t, dir)

	cmd, _ := newInstallCmd()
	if err := runInstall(cmd, installOptions{uninstall: true}); err != nil {
		t.Fatalf("runInstall --uninstall: %v", err)
	}

	for _, u := range units {
		if _, err := os.Stat(filepath.Join(dir, u.name)); !os.IsNotExist(err) {
			t.Errorf("%s still present", u.name)
		}
	}

	want := [][]string{
		{"systemctl", "--user", "disable", "--now", brokerSocketUnitName},
		{"systemctl", "--user", "stop", brokerServiceUnitName},
		{"systemctl", "--user", "daemon-reload"},
	}
	if len(*calls) != len(want) {
		t.Fatalf("expected %d systemctl calls, got %d: %v", len(want), len(*calls), *calls)
	}
	for i, w := range want {
		if strings.Join((*calls)[i], " ") != strings.Join(w, " ") {
			t.Errorf("call %d = %v, want %v", i, (*calls)[i], w)
		}
	}
}

func TestUninstallToleratesMissingUnits(t *testing.T) {
	dir := t.TempDir()
	withInstallSeams(t, dir)

	cmd, _ := newInstallCmd()
	if err := runInstall(cmd, installOptions{uninstall: true}); err != nil {
		t.Fatalf("runInstall --uninstall with no units: %v", err)
	}
}

func TestBrokerServiceUnitRejectsUnsafePath(t *testing.T) {
	if _, err := brokerServiceUnitFor("relative/ai-agent-broker"); err == nil {
		t.Fatal("expected relative broker path to be rejected")
	}
	if _, err := brokerServiceUnitFor("/tmp/ai agent/bin/ai-agent-broker"); err == nil {
		t.Fatal("expected whitespace broker path to be rejected")
	}
}

func TestEmbeddedUnitsMatchContrib(t *testing.T) {
	root := repoRoot(t)
	t.Setenv(paths.EnvBrokerSocket, "")
	units, err := brokerUnitsWithService(brokerServiceUnit)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range units {
		onDisk, err := os.ReadFile(filepath.Join(root, "contrib", "systemd", u.name))
		if err != nil {
			t.Fatalf("read contrib unit %s: %v", u.name, err)
		}
		if string(onDisk) != u.contents {
			t.Errorf("%s drifted from the embedded constant in install.go", u.name)
		}
	}
}

func TestBrokerUnitsFollowTheConfiguredSocket(t *testing.T) {
	t.Setenv(paths.EnvBrokerSocket, "")
	units, err := brokerUnitsWithService(brokerServiceUnit)
	if err != nil {
		t.Fatalf("brokerUnitsWithService: %v", err)
	}
	if !strings.Contains(units[0].contents, "ListenStream="+defaultListenStream) {
		t.Fatalf("default socket unit must keep the runtime-directory template:\n%s", units[0].contents)
	}
	if strings.Contains(units[1].contents, paths.EnvBrokerSocket+"=") {
		t.Fatalf("default service unit must not pin a socket env:\n%s", units[1].contents)
	}

	t.Setenv(paths.EnvBrokerSocket, "/srv/ai-agent/custom.sock")
	units, err = brokerUnitsWithService(brokerServiceUnit)
	if err != nil {
		t.Fatalf("brokerUnitsWithService: %v", err)
	}
	if !strings.Contains(units[0].contents, "ListenStream=/srv/ai-agent/custom.sock") {
		t.Fatalf("socket unit must bind the configured socket:\n%s", units[0].contents)
	}
	if !strings.Contains(units[1].contents, "Environment="+paths.EnvBrokerSocket+"=/srv/ai-agent/custom.sock") {
		t.Fatalf("service unit must pass the configured socket to the daemon:\n%s", units[1].contents)
	}

	t.Setenv(paths.EnvBrokerSocket, "relative/custom.sock")
	if _, err := brokerUnitsWithService(brokerServiceUnit); err == nil {
		t.Fatal("relative configured socket must fail unit generation")
	}
}
