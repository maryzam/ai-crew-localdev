package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/maryzam/ai-crew-localdev/internal/app/readiness"
)

const docPath = "docs/guide/cli-reference.md"

func main() {
	if err := generate(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func generate() error {
	data, err := os.ReadFile(docPath)
	if err != nil {
		return err
	}
	content := string(data)
	begin := strings.Index(content, readiness.DocBeginMarker)
	end := strings.Index(content, readiness.DocEndMarker)
	if begin < 0 || end < 0 {
		return fmt.Errorf("%s: readiness-checks markers not found", docPath)
	}
	afterBegin := begin + len(readiness.DocBeginMarker) + 1
	result := content[:afterBegin] + readiness.DocMarkdown() + content[end:]
	if result == content {
		return nil
	}
	return os.WriteFile(docPath, []byte(result), 0o644)
}
