package cli

import (
	"strings"
	"testing"
)

func TestTerminalTextEscapesControlsAndBoundsOutput(t *testing.T) {
	input := "unsafe\x1b[31m\n" + strings.Repeat("x", terminalTextLimit*2)
	got := terminalText(input)
	if strings.ContainsAny(got, "\x1b\n") || !strings.Contains(got, `\x1b`) || !strings.Contains(got, `\x0a`) {
		t.Fatalf("terminal text = %q", got)
	}
	if len(got) > terminalTextLimit+len("…") || !strings.HasSuffix(got, "…") {
		t.Fatalf("terminal text length = %d, suffix = %q", len(got), got[len(got)-3:])
	}
}
