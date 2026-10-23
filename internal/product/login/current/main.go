package main

import (
	"fmt"
	"os"

	"github.com/maryzam/ai-crew-localdev/internal/product/agents"
	"github.com/maryzam/ai-crew-localdev/internal/product/login"
)

func main() {
	if len(os.Args) != 3 {
		_, _ = fmt.Fprintln(os.Stderr, "usage: current <agent> credential|refresh-binary|refresh-binary-sha256")
		os.Exit(2)
	}
	credential, err := login.Current(agents.Default(), os.Args[1])
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "current %s login: %v\n", os.Args[1], err)
		os.Exit(1)
	}
	fields := map[string]string{"credential": credential.Path, "refresh-binary": credential.RefreshBinary, "refresh-binary-sha256": credential.RefreshBinarySHA256}
	value := fields[os.Args[2]]
	if value == "" {
		_, _ = fmt.Fprintf(os.Stderr, "%s login has no %s\n", os.Args[1], os.Args[2])
		os.Exit(2)
	}
	fmt.Println(value)
}
