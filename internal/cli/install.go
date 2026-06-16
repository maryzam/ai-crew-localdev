package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/spf13/cobra"
)

var installUninstall bool

var installCmd = &cobra.Command{
	Use:   "install",
	Short: "Install the broker systemd user units",
	Long: `Installs the ai-agent broker systemd user units into the user unit
directory, reloads the user systemd manager, and enables the broker socket
for socket activation. This replaces the manual broker-service install step.

Use --uninstall to disable and remove the units.`,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE:          runInstall,
}

func init() {
	installCmd.Flags().BoolVar(&installUninstall, "uninstall", false, "disable and remove the broker systemd user units")
}

// brokerSocketUnit and brokerServiceUnit mirror contrib/systemd/. Kept in sync
// with those files; the socket path uses systemd's %t (runtime dir) specifier.
const (
	brokerSocketUnitName = "ai-agent-broker.socket"
	brokerSocketUnit     = `[Unit]
Description=AI Agent Broker Socket

[Socket]
ListenStream=%t/ai-agent/broker.sock
SocketMode=0600
DirectoryMode=0700

[Install]
WantedBy=sockets.target
`

	brokerServiceUnitName = "ai-agent-broker.service"
	brokerServiceUnit     = `[Unit]
Description=AI Agent Authentication Broker
Requires=ai-agent-broker.socket

[Service]
Type=simple
ExecStart=%h/.local/bin/ai-agent-broker
Environment=AI_AGENT_CONFIG_DIR=%h/.config/ai-agent
Restart=on-failure
RestartSec=5

[Install]
WantedBy=default.target
`
)

// Test seams.
var (
	installUnitDir = defaultSystemdUserDir
	installRunCmd  = func(c *exec.Cmd) error { return c.Run() }
)

func defaultSystemdUserDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".config", "systemd", "user"), nil
}

type brokerUnit struct {
	name     string
	contents string
}

func brokerUnits() []brokerUnit {
	return []brokerUnit{
		{brokerSocketUnitName, brokerSocketUnit},
		{brokerServiceUnitName, brokerServiceUnit},
	}
}

func runInstall(cmd *cobra.Command, _ []string) error {
	if installUninstall {
		return uninstallUnits(cmd)
	}
	return installUnits(cmd)
}

func installUnits(cmd *cobra.Command) error {
	w := cmd.OutOrStdout()
	dir, err := installUnitDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create unit dir %s: %w", dir, err)
	}

	for _, u := range brokerUnits() {
		path := filepath.Join(dir, u.name)
		if err := os.WriteFile(path, []byte(u.contents), 0o600); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
		_, _ = fmt.Fprintf(w, "installed %s\n", path)
	}

	if err := systemctl(cmd, "daemon-reload"); err != nil {
		return err
	}
	if err := systemctl(cmd, "enable", brokerSocketUnitName); err != nil {
		return err
	}

	_, _ = fmt.Fprintln(w, "broker units installed; start now with: systemctl --user start ai-agent-broker.socket")
	return nil
}

func uninstallUnits(cmd *cobra.Command) error {
	w := cmd.OutOrStdout()
	dir, err := installUnitDir()
	if err != nil {
		return err
	}

	_ = systemctl(cmd, "disable", brokerSocketUnitName)

	for _, u := range brokerUnits() {
		path := filepath.Join(dir, u.name)
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove %s: %w", path, err)
		}
		_, _ = fmt.Fprintf(w, "removed %s\n", path)
	}

	if err := systemctl(cmd, "daemon-reload"); err != nil {
		return err
	}

	_, _ = fmt.Fprintln(w, "broker units uninstalled")
	return nil
}

func systemctl(cmd *cobra.Command, args ...string) error {
	full := append([]string{"--user"}, args...)
	c := exec.Command("systemctl", full...)
	c.Stdout = cmd.OutOrStdout()
	c.Stderr = cmd.OutOrStderr()
	if err := installRunCmd(c); err != nil {
		return fmt.Errorf("systemctl %v: %w", full, err)
	}
	return nil
}
