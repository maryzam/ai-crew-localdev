package cli

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/maryzam/ai-crew-localdev/internal/broker"
	"github.com/maryzam/ai-crew-localdev/internal/brokerclient"
	"github.com/maryzam/ai-crew-localdev/internal/config"
	"github.com/maryzam/ai-crew-localdev/internal/launcher"
	"github.com/spf13/cobra"
)

var sessionCmd = &cobra.Command{
	Use:   "session",
	Short: "Manage agent sessions",
}

// ---- session revoke ---------------------------------------------------------

var sessionRevokeCmd = &cobra.Command{
	Use:   "revoke <session-id>",
	Short: "Revoke an active session",
	Args:  cobra.ExactArgs(1),
	RunE:  runSessionRevoke,
}

var revokeSocketPath string
var removeSessionInfo = launcher.RemoveSessionInfo

func init() {
	sessionRevokeCmd.Flags().StringVar(&revokeSocketPath, "broker-sock", "", "broker socket path (default: auto)")
}

func runSessionRevoke(cmd *cobra.Command, args []string) error {
	sessionID := args[0]

	info, err := launcher.LoadSessionInfo(sessionID)
	if err != nil {
		return fmt.Errorf("load session info: %w\nHint: session files are stored in %s/sessions/",
			err, config.RuntimeDir())
	}

	socketPath := revokeSocketPath
	if socketPath == "" {
		socketPath = info.SocketPath
	}
	if socketPath == "" {
		socketPath = config.DefaultSocketPath()
	}

	client := &brokerclient.Client{SocketPath: socketPath}
	if err := client.RevokeSession(broker.RevokeSessionRequest{
		SessionID:  sessionID,
		BindSecret: info.BindSecret,
	}); err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}

	// Clean up session file.
	if err := cleanupRevokedSession(sessionID); err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "session %s revoked\n", sessionID)
	return nil
}

func cleanupRevokedSession(sessionID string) error {
	if err := removeSessionInfo(sessionID); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("session revoked but failed to remove local session file: %w", err)
	}
	return nil
}

// ---- session status ---------------------------------------------------------

var sessionStatusCmd = &cobra.Command{
	Use:   "status <session-id>",
	Short: "Show the status of a session",
	Args:  cobra.ExactArgs(1),
	RunE:  runSessionStatus,
}

var statusSocketPath string

func init() {
	sessionStatusCmd.Flags().StringVar(&statusSocketPath, "broker-sock", "", "broker socket path (default: auto)")
}

func runSessionStatus(cmd *cobra.Command, args []string) error {
	sessionID := args[0]

	info, err := launcher.LoadSessionInfo(sessionID)
	if err != nil {
		return fmt.Errorf("load session info: %w", err)
	}

	socketPath := statusSocketPath
	if socketPath == "" {
		socketPath = info.SocketPath
	}
	if socketPath == "" {
		socketPath = config.DefaultSocketPath()
	}

	client := &brokerclient.Client{SocketPath: socketPath}
	status, err := client.SessionStatus(broker.SessionStatusRequest{
		SessionID:  sessionID,
		BindSecret: info.BindSecret,
	})
	if err != nil {
		return fmt.Errorf("get session status: %w", err)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintf(w, "Session ID:\t%s\n", sessionID)
	fmt.Fprintf(w, "Active:\t%v\n", status.Active)
	fmt.Fprintf(w, "Agent:\t%s\n", status.AgentName)
	fmt.Fprintf(w, "Repo:\t%s\n", status.Repo)
	fmt.Fprintf(w, "Created:\t%s\n", status.CreatedAt.Local().Format("2006-01-02 15:04:05"))
	fmt.Fprintf(w, "Expires:\t%s\n", status.ExpiresAt.Local().Format("2006-01-02 15:04:05"))
	fmt.Fprintf(w, "Last Activity:\t%s\n", status.LastActivity.Local().Format("2006-01-02 15:04:05"))
	fmt.Fprintf(w, "Token Mints:\t%d\n", status.TokenMintsCount)
	w.Flush()

	return nil
}

// ---- session list -----------------------------------------------------------

var sessionListCmd = &cobra.Command{
	Use:   "list",
	Short: "List known sessions",
	RunE:  runSessionList,
}

func runSessionList(cmd *cobra.Command, args []string) error {
	ids, err := launcher.ListSessionFiles()
	if err != nil {
		return fmt.Errorf("list sessions: %w", err)
	}

	if len(ids) == 0 {
		fmt.Println("no active sessions")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintf(w, "SESSION ID\tAGENT\tREPO\n")
	for _, id := range ids {
		info, err := launcher.LoadSessionInfo(id)
		if err != nil {
			fmt.Fprintf(w, "%s\t(error loading)\t\n", id)
			continue
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", id, info.AgentName, info.Repo)
	}
	w.Flush()

	return nil
}
