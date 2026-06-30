package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/maryzam/ai-crew-localdev/internal/brokerapi"
	"github.com/maryzam/ai-crew-localdev/internal/brokerclient"
	githubcontract "github.com/maryzam/ai-crew-localdev/internal/providers/github/contract"
	"github.com/maryzam/ai-crew-localdev/internal/sessionauth"
)

func main() {
	if len(os.Args) < 2 {
		die("usage: ai-agent-credential-helper <get|store|erase>")
	}

	op := os.Args[1]

	if op != "get" {
		os.Exit(0)
	}

	if err := handleGet(); err != nil {
		die("error: %v", err)
	}
}

func handleGet() error {
	fields := parseCredentialInput()

	host := fields["host"]
	if host != "github.com" && host != "" {
		return nil
	}

	protocol := fields["protocol"]
	if protocol != "https" && protocol != "" {
		return nil
	}

	path := fields["path"]
	repo := strings.TrimSuffix(path, ".git")
	if repo == "" {
		return fmt.Errorf("no path in credential request; cannot determine repository")
	}

	session, err := sessionauth.Load()
	if err != nil {
		return err
	}

	client := &brokerclient.Client{SocketPath: session.SocketPath}
	resp, err := client.MintCredential(brokerapi.CredentialRequest{
		SessionID:      session.SessionID,
		BindSecret:     session.BindSecret,
		CredentialType: githubcontract.CredentialType,
		Resource:       "github:repo:" + repo,
	})
	if err != nil {
		return fmt.Errorf("mint credential: %w", err)
	}

	var ghCred githubcontract.Credential
	if err := json.Unmarshal(resp.Credential, &ghCred); err != nil {
		return fmt.Errorf("decode github credential payload: %w", err)
	}

	fmt.Printf("protocol=https\n")
	fmt.Printf("host=github.com\n")
	fmt.Printf("username=x-access-token\n")
	fmt.Printf("password=%s\n", ghCred.Token)
	fmt.Printf("password_expiry_utc=%d\n", resp.ExpiresAt.Unix())
	fmt.Printf("\n")

	return nil
}

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
