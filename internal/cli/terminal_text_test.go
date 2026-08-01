package cli

import (
	"strings"
	"testing"
)

func TestTerminalTextEscapesControlsAndBoundsOutput(t *testing.T) {
	input := "unsafe\x1b[31m\n\u009b\u202e" + strings.Repeat("x", terminalTextLimit*2)
	got := terminalText(input)
	if strings.ContainsAny(got, "\x1b\n\u009b\u202e") || !strings.Contains(got, `\u{1b}`) || !strings.Contains(got, `\u{a}`) || !strings.Contains(got, `\u{9b}`) || !strings.Contains(got, `\u{202e}`) {
		t.Fatalf("terminal text = %q", got)
	}
	if len(got) > terminalTextLimit+len("…") || !strings.HasSuffix(got, "…") {
		t.Fatalf("terminal text length = %d, suffix = %q", len(got), got[len(got)-3:])
	}
}
