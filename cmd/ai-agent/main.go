package main

import (
	"os"

	"github.com/maryzam/ai-crew-localdev/internal/cli"
)

func main() {
	if err := cli.Execute(); err != nil {
		os.Exit(1)
	}
}
