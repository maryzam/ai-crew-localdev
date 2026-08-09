package cli

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/spf13/cobra"
)

const terminalTextLimit = 2048

func terminalText(value string) string {
	var output strings.Builder
	truncated := false
	for _, character := range value {
		encoded := string(character)
		if character < 0x20 || character >= 0x7f && character <= 0x9f || unicode.In(character, unicode.Cf) {
			encoded = fmt.Sprintf("\\u{%x}", character)
		}
		if output.Len()+len(encoded) > terminalTextLimit {
			truncated = true
			break
		}
		output.WriteString(encoded)
	}
	if truncated {
		output.WriteString("…")
	}
	return output.String()
}

func renderCLIError(command *cobra.Command, operation string, err error) {
	_, _ = fmt.Fprintf(command.ErrOrStderr(), "%s failed: %s\n", operation, terminalText(err.Error()))
}
