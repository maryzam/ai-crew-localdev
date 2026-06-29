package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"

	"github.com/maryzam/ai-crew-localdev/internal/telemetry"
)

func main() {
	check := flag.Bool("check", false, "fail if the generated schema differs from the committed document")
	output := flag.String("output", "docs/managed-run-telemetry-schema.md", "schema document path")
	flag.Parse()

	generated := []byte(telemetry.SchemaReferenceMarkdown())
	if *check {
		committed, err := os.ReadFile(*output)
		if err != nil {
			fatal(err)
		}
		if !bytes.Equal(committed, generated) {
			fatal(fmt.Errorf("%s is stale; run go run ./cmd/telemetry-schema", *output))
		}
		return
	}
	if err := os.WriteFile(*output, generated, 0o644); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	_, _ = fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}
