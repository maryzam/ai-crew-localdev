// ai-agent-credential-helper is a git credential helper that obtains
// tokens from the ai-agent broker.
//
// It is invoked by git as:
//   ai-agent-credential-helper get
//   ai-agent-credential-helper store   (no-op)
//   ai-agent-credential-helper erase   (no-op)
//
// On "get", it reads the git credential protocol from stdin, extracts
// the repository path, reads the session bind secret from the inherited
// FD, and requests a token from the broker.
//
// This helper must never have access to PEM material. It only communicates
// with the broker over the Unix socket.
package main

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/maryzam/ai-crew-localdev/internal/broker"
	"github.com/maryzam/ai-crew-localdev/internal/brokerclient"
	"github.com/maryzam/ai-crew-localdev/internal/launcher"
)

func main() {
	if len(os.Args) < 2 {
		die("usage: ai-agent-credential-helper <get|store|erase>")
	}

	op := os.Args[1]

	// Only "get" needs broker interaction. "store" and "erase" are no-ops
	// because the broker manages token lifecycle.
	if op != "get" {
		os.Exit(0)
	}

	if err := handleGet(); err != nil {
		die("error: %v", err)
	}
}

func handleGet() error {
	// Read git credential protocol from stdin.
	fields := parseCredentialInput()

	// We only handle github.com HTTPS requests.
	host := fields["host"]
	if host != "github.com" && host != "" {
		// Not our host; exit silently so git can try other helpers.
		return nil
	}

	protocol := fields["protocol"]
	if protocol != "https" && protocol != "" {
		return nil
	}

	// Extract repo slug from the path field.
	// git sends path=owner/repo.git (or owner/repo).
	path := fields["path"]
	repo := strings.TrimSuffix(path, ".git")
	if repo == "" {
		return fmt.Errorf("no path in credential request; cannot determine repository")
	}

	// Read session metadata from environment.
	socketPath := os.Getenv("AI_AGENT_AUTH_SOCK")
	if socketPath == "" {
		return fmt.Errorf("AI_AGENT_AUTH_SOCK not set; not in a managed session")
	}

	sessionID := os.Getenv("AI_AGENT_SESSION_ID")
	if sessionID == "" {
		return fmt.Errorf("AI_AGENT_SESSION_ID not set; not in a managed session")
	}

	bindFDStr := os.Getenv("AI_AGENT_SESSION_BIND_FD")
	if bindFDStr == "" {
		return fmt.Errorf("AI_AGENT_SESSION_BIND_FD not set; not in a managed session")
	}
	bindFD, err := strconv.Atoi(bindFDStr)
	if err != nil {
		return fmt.Errorf("invalid AI_AGENT_SESSION_BIND_FD: %w", err)
	}

	// Read bind secret from FD (via /proc/self/fd/N reopen).
	bindSecret, err := launcher.ReadBindSecret(bindFD)
	if err != nil {
		return fmt.Errorf("read bind secret: %w", err)
	}

	// Request token from broker.
	client := &brokerclient.Client{SocketPath: socketPath}
	resp, err := client.MintToken(broker.TokenRequest{
		SessionID:  sessionID,
		BindSecret: bindSecret,
		Repo:       repo,
	})
	if err != nil {
		return fmt.Errorf("mint token: %w", err)
	}

	// Output git credential protocol response.
	fmt.Printf("protocol=https\n")
	fmt.Printf("host=github.com\n")
	fmt.Printf("username=x-access-token\n")
	fmt.Printf("password=%s\n", resp.Token)
	fmt.Printf("password_expiry_utc=%d\n", resp.ExpiresAt.Unix())
	fmt.Printf("\n")

	return nil
}

// parseCredentialInput reads key=value pairs from stdin until an empty line
// or EOF, as per the git credential protocol.
func parseCredentialInput() map[string]string {
	fields := make(map[string]string)
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			break
		}
		k, v, ok := strings.Cut(line, "=")
		if ok {
			fields[k] = v
		}
	}
	return fields
}

func die(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "ai-agent-credential-helper: "+format+"\n", args...)
	os.Exit(1)
}
