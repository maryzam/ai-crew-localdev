package main

import (
	"fmt"
	"os"

	"github.com/maryzam/ai-crew-localdev/internal/product/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "ai: %v\n", err)
		os.Exit(1)
	}
}
