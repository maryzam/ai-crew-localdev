package cli

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/maryzam/ai-crew-localdev/internal/broker/api"
	brokerclient "github.com/maryzam/ai-crew-localdev/internal/broker/client"
	"github.com/maryzam/ai-crew-localdev/internal/platform/paths"
	sessionstore "github.com/maryzam/ai-crew-localdev/internal/runtime/session"
	"github.com/spf13/cobra"
)

var sessionCmd = &cobra.Command{
	Use:   "session",
	Short: "Manage agent sessions",
}

var sessionRevokeCmd = &cobra.Command{
	Use:   "revoke <session-id>",
	Short: "Revoke an active session",
	Args:  cobra.ExactArgs(1),
	RunE:  runSessionRevoke,
}

var revokeSocketPath string
var loadSessionInfo = sessionstore.LoadInfo
var listSessionInfo = sessionstore.ListInfo
var removeSessionInfo = sessionstore.RemoveInfo

func init() {
	sessionRevokeCmd.Flags().StringVar(&revokeSocketPath, "broker-sock", "", "broker socket path (default: auto)")
}

func runSessionRevoke(cmd *cobra.Command, args []string) error {
	sessionID := args[0]

	info, err := loadSessionInfo(sessionID)
	if err != nil {
		return fmt.Errorf("load session info: %w\nHint: session files are stored in %s/sessions/",
			err, paths.RuntimeDir())
	}

	socketPath := revokeSocketPath
	socketPath, err = resolveSessionBrokerSocketPath(socketPath, info.SocketPath)
	if err != nil {
		return err
	}

	client := &brokerclient.Client{SocketPath: socketPath}
	if err := client.RevokeSession(api.RevokeSessionRequest{
		SessionID: sessionID,
	}); err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}

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

	info, err := loadSessionInfo(sessionID)
	if err != nil {
		return fmt.Errorf("load session info: %w", err)
	}

	socketPath := statusSocketPath
	socketPath, err = resolveSessionBrokerSocketPath(socketPath, info.SocketPath)
	if err != nil {
		return err
	}

	client := &brokerclient.Client{SocketPath: socketPath}
	status, err := client.SessionStatus(api.SessionStatusRequest{
		SessionID: sessionID,
	})
	if err != nil {
		return fmt.Errorf("get session status: %w", err)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintf(w, "Session ID:\t%s\n", sessionID)
	_, _ = fmt.Fprintf(w, "Active:\t%v\n", status.Active)
	_, _ = fmt.Fprintf(w, "Agent:\t%s\n", status.AgentName)
	for i, r := range status.Resources {
		label := "Resources:"
		if i > 0 {
			label = ""
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\n", label, r)
	}
	_, _ = fmt.Fprintf(w, "Created:\t%s\n", status.CreatedAt.Local().Format("2006-01-02 15:04:05"))
	_, _ = fmt.Fprintf(w, "Expires:\t%s\n", status.ExpiresAt.Local().Format("2006-01-02 15:04:05"))
	_, _ = fmt.Fprintf(w, "Last Activity:\t%s\n", status.LastActivity.Local().Format("2006-01-02 15:04:05"))
	_, _ = fmt.Fprintf(w, "Mints:\t%d\n", status.MintCount)
	_ = w.Flush()

	return nil
}

var sessionListCmd = &cobra.Command{
	Use:   "list",
	Short: "List known sessions",
	RunE:  runSessionList,
}

func runSessionList(cmd *cobra.Command, args []string) error {
	ids, err := listSessionInfo()
	if err != nil {
		return fmt.Errorf("list sessions: %w", err)
	}

	if len(ids) == 0 {
		fmt.Println("no active sessions")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintf(w, "SESSION ID\tAGENT\tREPO\n")
	for _, id := range ids {
		info, err := loadSessionInfo(id)
		if err != nil {
			_, _ = fmt.Fprintf(w, "%s\t(error loading)\t\n", id)
			continue
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\n", id, info.AgentName, info.Repo)
	}
	_ = w.Flush()

	return nil
}
